// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/racing/sources"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestSlowCandidateMetadataDoesNotBlockCompleteCandidate(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var downloads atomic.Int32
	data := []byte("d4:infod6:lengthi1024e4:name14:Example Aurora12:piece lengthi16384e6:pieces20:abcdefghijklmnopqrstee")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download" {
			downloads.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = w.Write(data)
			return
		}
		_, _ = w.Write([]byte(`<rss><channel/></rss>`))
	}))
	defer server.Close()
	db := testdb.NewMigratedSQLite(t, "racing-metadata-runtime")
	store, err := models.NewRacingStore(db, bytes.Repeat([]byte{1}, 32))
	require.NoError(t, err)
	site, err := store.SaveSite(t.Context(), 0, models.RacingSiteInput{Name: "Synthetic site", BaseURL: server.URL, Enabled: true, RequestIntervalSeconds: 1})
	require.NoError(t, err)
	endpoint := server.URL + "/rss"
	sourceID, err := store.SaveSource(t.Context(), 0, models.RacingSourceInput{Name: "Synthetic source", Kind: "rss", Adapter: "chd", Enabled: true, SiteID: site, IntervalSeconds: 60, URL: &endpoint})
	require.NoError(t, err)
	group, err := store.SaveGroup(t.Context(), 0, models.RacingGroupInput{Name: "Empty target", Enabled: true})
	require.NoError(t, err)
	minimum := int64(1000)
	_, err = store.SaveRule(t.Context(), 0, models.RacingRuleInput{Name: "Needs exact size", Enabled: true, SourceIDs: []int{sourceID}, AcceptKinds: []string{"official"}, ReceiveWindowSeconds: 600, TargetGroupID: &group, Filters: models.RacingRuleFilters{MinSizeBytes: &minimum}})
	require.NoError(t, err)
	inputs, err := store.RuntimeSources(t.Context())
	require.NoError(t, err)
	source := inputs[0]
	published := time.Now().UTC().Add(-time.Minute)
	size := int64(1024)
	first := sources.PublicItem{TorrentID: "42", EventKey: "torrent:42:published", Title: "Example Aurora", SizeBytes: &size, SizeApproximate: true, PublishedAt: &published, Official: sources.Evidence{Value: sources.Yes}, Free: sources.Evidence{Value: sources.Unknown}, Revival: sources.Evidence{Value: sources.No}}
	second := first
	second.TorrentID = "43"
	second.EventKey = "torrent:43:published"
	second.SizeApproximate = false
	transport, err := json.Marshal(struct {
		DownloadURL string `json:"downloadUrl"`
	}{server.URL + "/download?passkey=synthetic-private"})
	require.NoError(t, err)
	for _, item := range []sources.PublicItem{first, second} {
		public, err := json.Marshal(item)
		require.NoError(t, err)
		require.NoError(t, store.SaveDiscovery(t.Context(), source, item.EventKey, public, transport, true))
	}
	service := NewService(store)
	require.NoError(t, service.Start(t.Context()))
	defer service.Stop()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("necessary metadata did not start")
	}
	require.Eventually(t, func() bool {
		records, err := store.CandidateRecords(t.Context(), "", nil, 100)
		if err != nil || len(records) != 2 {
			return false
		}
		var slow, fast string
		for _, record := range records {
			if record.EventKey == first.EventKey {
				slow = record.State
			} else {
				fast = record.State
			}
		}
		return slow == "waiting_metadata" && fast == "waiting_target"
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, int32(1), downloads.Load(), "complete candidate must not request metadata or promotion expiry")
	close(release)
	require.Eventually(t, func() bool {
		records, err := store.CandidateRecords(t.Context(), "", nil, 100)
		if err != nil || len(records) != 2 {
			return false
		}
		for _, record := range records {
			if record.EventKey != first.EventKey {
				continue
			}
			var candidate Candidate
			_ = json.Unmarshal(record.Candidate, &candidate)
			return record.State == "waiting_target" && candidate.VerifiedMetadata != nil && candidate.VerifiedMetadata.HashV1 != "" && !candidate.Item.SizeApproximate
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
	service.Stop()
	restarted := NewService(store)
	require.NoError(t, restarted.Start(t.Context()))
	defer restarted.Stop()
	require.Eventually(t, func() bool {
		records, err := store.CandidateRecords(t.Context(), "", nil, 100)
		return err == nil && len(records) == 2
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, int32(1), downloads.Load())
}
