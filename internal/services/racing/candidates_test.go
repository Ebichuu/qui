// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/racing/sources"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestCandidatePersistenceConcurrentSourcesAndConfigurationChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`<rss><channel/></rss>`)) }))
	defer server.Close()
	db := testdb.NewMigratedSQLite(t, "racing-candidate-runtime")
	key := bytes.Repeat([]byte{2}, 32)
	store, err := models.NewRacingStore(db, key)
	require.NoError(t, err)
	site, err := store.SaveSite(t.Context(), 0, models.RacingSiteInput{Name: "Synthetic site", BaseURL: server.URL, Enabled: true, RequestIntervalSeconds: 1})
	require.NoError(t, err)
	endpoint := server.URL + "/rss"
	for range 2 {
		_, err = store.SaveSource(t.Context(), 0, models.RacingSourceInput{Name: "Synthetic source", Kind: "rss", Adapter: "chd", Enabled: true, SiteID: site, IntervalSeconds: 60, URL: &endpoint})
		require.NoError(t, err)
	}
	runtime, err := store.RuntimeSources(t.Context())
	require.NoError(t, err)
	require.Len(t, runtime, 2)
	group, err := store.SaveGroup(t.Context(), 0, models.RacingGroupInput{Name: "Empty selected group", Enabled: true})
	require.NoError(t, err)
	free := models.RacingRuleInput{Name: "Free fallback", Enabled: true, SourceIDs: []int{runtime[0].ID, runtime[1].ID}, AcceptKinds: []string{"free"}, ReceiveWindowSeconds: 900, TargetGroupID: &group, AllowOfficialReclaim: true}
	freeID, err := store.SaveRule(t.Context(), 0, free)
	require.NoError(t, err)
	official := free
	official.Name = "Official winner"
	official.SortOrder = 99
	official.AcceptKinds = []string{"official"}
	official.AllowOfficialReclaim = false
	officialID, err := store.SaveRule(t.Context(), 0, official)
	require.NoError(t, err)
	service := NewService(store)
	require.NoError(t, service.Start(t.Context()))
	defer service.Stop()
	published := time.Now().UTC().Add(-30 * time.Second)
	item := sources.PublicItem{TorrentID: "42", EventKey: "torrent:42:published", Title: "Example Aurora Full Synthetic Title", PublishedAt: &published, Official: sources.Evidence{Value: sources.Yes}, Free: sources.Evidence{Value: sources.Yes}, Revival: sources.Evidence{Value: sources.No}}
	public, err := json.Marshal(item)
	require.NoError(t, err)
	errors := make(chan error, 2)
	var writes sync.WaitGroup
	for _, source := range runtime {
		writes.Go(func() { errors <- store.SaveDiscovery(t.Context(), source, item.EventKey, public, []byte(`{}`), true) })
	}
	writes.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var initial models.RacingCandidateRecord
	require.Eventually(t, func() bool {
		rows, err := store.CandidateRecords(t.Context(), "", nil, 100)
		if err != nil || len(rows) != 1 {
			return false
		}
		initial = rows[0]
		var candidate Candidate
		var selection RuleSelection
		if json.Unmarshal(initial.Candidate, &candidate) != nil || json.Unmarshal(initial.Selection, &selection) != nil {
			return false
		}
		return len(candidate.SourceIDs) == 2 && selection.Rule != nil && selection.Rule.ID == officialID
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, "waiting_target", initial.State)
	var selected RuleSelection
	require.NoError(t, json.Unmarshal(initial.Selection, &selected))
	require.False(t, selected.Rule.AllowOfficialReclaim)
	// A configuration edit reselects the whole rule and retains event identity.
	official.Enabled = false
	_, err = store.SaveRule(t.Context(), officialID, official)
	require.NoError(t, err)
	service.ConfigurationChanged()
	require.Eventually(t, func() bool {
		rows, err := store.CandidateRecords(t.Context(), "", nil, 100)
		if err != nil || len(rows) != 1 {
			return false
		}
		_ = json.Unmarshal(rows[0].Selection, &selected)
		return selected.Rule != nil && selected.Rule.ID == freeID && selected.Rule.AllowOfficialReclaim
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, "official", selected.Priority)
	service.Stop()
	restarted := NewService(store)
	require.NoError(t, restarted.Start(t.Context()))
	defer restarted.Stop()
	require.Eventually(t, func() bool {
		rows, err := store.PendingDiscoveries(t.Context(), 0, 0, 100)
		return err == nil && len(rows.Items) == 0
	}, 5*time.Second, 10*time.Millisecond)
	records, err := store.CandidateRecords(t.Context(), "", nil, 100)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, initial.Key, records[0].Key)
	require.Equal(t, initial.FirstSeenAt, records[0].FirstSeenAt)
	// Removing an arrival source must not reset the event's initial discovery.
	for _, id := range []int{freeID, officialID} {
		require.NoError(t, store.Delete(t.Context(), "rules", id))
	}
	require.NoError(t, store.Delete(t.Context(), "sources", runtime[0].ID))
	restarted.ConfigurationChanged()
	require.Eventually(t, func() bool {
		records, err := store.CandidateRecords(t.Context(), "", nil, 100)
		if err != nil || len(records) != 1 {
			return false
		}
		var candidate Candidate
		_ = json.Unmarshal(records[0].Candidate, &candidate)
		return len(candidate.SourceIDs) == 1 && candidate.FirstSeenAt.Format(time.RFC3339Nano) == initial.FirstSeenAt
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, store.Delete(t.Context(), "sources", runtime[1].ID))
	restarted.ConfigurationChanged()
	require.Eventually(t, func() bool {
		records, err := store.CandidateRecords(t.Context(), "", nil, 100)
		if err != nil || len(records) != 1 {
			return false
		}
		_ = json.Unmarshal(records[0].Selection, &selected)
		return selected.Reason == "source_removed" && records[0].State == "rejected" && records[0].NextEvaluationAt == nil
	}, 5*time.Second, 10*time.Millisecond)
}
