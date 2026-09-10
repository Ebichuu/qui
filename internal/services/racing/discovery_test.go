// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestDiscoveryServiceIndependentWorkersAndRestart(t *testing.T) {
	var rssRequests atomic.Int32
	var cookieRequests atomic.Int32
	rssStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "sid=synthetic-private" {
			cookieRequests.Add(1)
		}
		switch r.URL.Path {
		case "/rss":
			rssRequests.Add(1)
			select {
			case rssStarted <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			panic(http.ErrAbortHandler)
		case "/torrents.php":
			if r.URL.Query().Get("page") == "1" {
				http.Error(w, "later page unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = fmt.Fprintf(w, `<table class="torrents"><tr><td><a href="details.php?id=42" title="Example Aurora Complete Long Title-CHD"><b>Example Aurora...</b></a><a href="download.php?id=42&amp;passkey=synthetic-private">Download</a></td><td>1 GiB</td><td class="rowfollow nowrap"><span title="%s">now</span></td></tr></table>`, time.Now().In(time.FixedZone("CHD", 8*3600)).Format("2006-01-02 15:04:05"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	db := testdb.NewMigratedSQLite(t, "racing-discovery-runtime")
	key := bytes.Repeat([]byte{3}, 32)
	store, err := models.NewRacingStore(db, key)
	require.NoError(t, err)
	cookie := "sid=synthetic-private"
	site, err := store.SaveSite(t.Context(), 0, models.RacingSiteInput{Name: "Synthetic site", BaseURL: server.URL, Enabled: true, RequestIntervalSeconds: 1, Credential: &cookie})
	require.NoError(t, err)
	rssURL := server.URL + "/rss"
	rssInput := models.RacingSourceInput{SiteID: site, Name: "Slow RSS", Kind: "rss", Adapter: "chd", Enabled: true, IntervalSeconds: 1, URL: &rssURL}
	rssID, err := store.SaveSource(t.Context(), 0, rssInput)
	require.NoError(t, err)
	webURL := server.URL + "/torrents.php"
	webInput := models.RacingSourceInput{SiteID: site, Name: "Fast web", Kind: "web", Adapter: "chd", Enabled: true, IntervalSeconds: 1, PageCount: 2, InitialLookbackSeconds: 60, URL: &webURL}
	webID, err := store.SaveSource(t.Context(), 0, webInput)
	require.NoError(t, err)
	service := NewService(store)
	require.NoError(t, service.Start(t.Context()))
	defer service.Stop()
	select {
	case <-rssStarted:
	case <-time.After(4 * time.Second):
		t.Fatal("RSS did not start")
	}
	var first models.RacingDiscovery
	require.Eventually(t, func() bool {
		rows, err := store.Discoveries(t.Context(), 100)
		if err != nil || len(rows) != 1 {
			return false
		}
		first = rows[0]
		return true
	}, 4*time.Second, 10*time.Millisecond, "web discovery waited for stalled RSS")
	require.Equal(t, webID, first.SourceID)
	require.True(t, first.Eligible)
	require.Eventually(t, func() bool {
		for _, status := range service.SourceStatuses() {
			if status.SourceID == webID {
				return status.LastError == "request_failed" && status.LastItemCount == 1
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond, "later page failure must retain first item")
	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "synthetic-private")
	require.Positive(t, cookieRequests.Load())
	// Disable while RSS is stalled. Reconciliation cancels that request and does
	// not wait for its normal timeout before removing the worker.
	rssInput.Enabled = false
	_, err = store.SaveSource(t.Context(), rssID, rssInput)
	require.NoError(t, err)
	service.ConfigurationChanged()
	require.Eventually(t, func() bool {
		for _, status := range service.SourceStatuses() {
			if status.SourceID == rssID {
				return false
			}
		}
		return true
	}, time.Second, 10*time.Millisecond)
	service.Stop()
	require.False(t, service.Status().Running)
	beforeRequests := rssRequests.Load()
	baseline, err := store.SourceBaseline(t.Context(), webID, 0)
	require.NoError(t, err)
	restartedStore, err := models.NewRacingStore(db, key)
	require.NoError(t, err)
	restarted := NewService(restartedStore)
	require.NoError(t, restarted.Start(t.Context()))
	defer restarted.Stop()
	require.Eventually(t, func() bool {
		rows, err := store.Discoveries(t.Context(), 100)
		return err == nil && len(rows) == 1 && rows[0].LastSeenAt != first.LastSeenAt
	}, 4*time.Second, 10*time.Millisecond)
	rows, err := store.Discoveries(t.Context(), 100)
	require.NoError(t, err)
	require.Equal(t, first.ID, rows[0].ID)
	require.Equal(t, first.FirstSeenAt, rows[0].FirstSeenAt)
	again, err := store.SourceBaseline(t.Context(), webID, 0)
	require.NoError(t, err)
	require.Equal(t, baseline, again)
	require.Equal(t, beforeRequests, rssRequests.Load())
}
