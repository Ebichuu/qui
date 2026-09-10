// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/database"
	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestRacingCandidateRevisionsAndMetadata(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var db *database.DB
			if engine == "sqlite" {
				db = testdb.NewMigratedSQLite(t, "racing-candidates")
			} else {
				db = testdb.NewMigratedPostgres(t, "racing-candidates")
			}
			store, err := models.NewRacingStore(db, bytes.Repeat([]byte{2}, 32))
			require.NoError(t, err)
			site, err := store.SaveSite(t.Context(), 0, models.RacingSiteInput{Name: "Synthetic site", BaseURL: "https://tracker.invalid", Enabled: true, RequestIntervalSeconds: 1})
			require.NoError(t, err)
			endpoint := "https://tracker.invalid/feed?passkey=synthetic-private"
			input := models.RacingSourceInput{SiteID: site, Name: "Synthetic RSS", Enabled: true, Kind: "rss", Adapter: "chd", IntervalSeconds: 10, URL: &endpoint}
			id, err := store.SaveSource(t.Context(), 0, input)
			require.NoError(t, err)
			inputs, err := store.RuntimeSources(t.Context())
			require.NoError(t, err)
			require.Len(t, inputs, 1)
			event := "torrent:42:published"
			require.NoError(t, store.SaveDiscovery(t.Context(), inputs[0], event, []byte(`{"title":"Example Aurora"}`), []byte(`{}`), true))
			pending, err := store.PendingDiscoveries(t.Context(), 0, 0, 100)
			require.NoError(t, err)
			require.Len(t, pending.Items, 1)
			// A newer observation arriving during evaluation must remain pending.
			require.NoError(t, store.SaveDiscovery(t.Context(), inputs[0], event, []byte(`{"title":"Example Aurora Full Title"}`), []byte(`{}`), true))
			next := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
			record := models.RacingCandidateRecord{Key: "site:1:torrent:42:published", SiteID: site, EventKey: event, FirstSeenAt: pending.Items[0].FirstSeenAt, Candidate: json.RawMessage(`{"title":"Example Aurora"}`), Selection: json.RawMessage(`{"state":"ready"}`), State: "ready", NextEvaluationAt: &next}
			require.NoError(t, store.SaveCandidate(t.Context(), record, pending.Items))
			latest, err := store.PendingDiscoveries(t.Context(), 0, 0, 100)
			require.NoError(t, err)
			require.Len(t, latest.Items, 1)
			require.Equal(t, int64(2), latest.Items[0].Revision)
			observed, err := store.EventDiscoveries(t.Context(), record.Key, site, event, 0)
			require.NoError(t, err)
			require.Len(t, observed.Observations, 1)
			require.Equal(t, record.FirstSeenAt, observed.FirstSeenAt)
			require.NoError(t, store.SaveCandidate(t.Context(), record, latest.Items))
			pending, err = store.PendingDiscoveries(t.Context(), 0, 0, 100)
			require.NoError(t, err)
			require.Empty(t, pending.Items)
			metadata := []byte(`{"name":"Example Aurora","sizeBytes":1024,"hashV1":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
			private := []byte("synthetic-torrent-bytes-with-private-announce")
			require.NoError(t, store.SaveCandidateMetadata(t.Context(), record.Key, inputs[0], metadata, private))
			proof, err := store.CandidateMetadata(t.Context(), record.Key)
			require.NoError(t, err)
			require.JSONEq(t, string(metadata), string(proof))
			var encrypted string
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT private_ciphertext FROM racing_candidate_metadata WHERE candidate_key=?`, record.Key).Scan(&encrypted))
			require.NotContains(t, encrypted, "synthetic-torrent")
			rows, err := store.CandidateRecords(t.Context(), "", nil, 100)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			encoded, err := json.Marshal(rows)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private-announce")
			due := time.Now().UTC().Format(time.RFC3339Nano)
			rows, err = store.CandidateRecords(t.Context(), "", &due, 100)
			require.NoError(t, err)
			require.Empty(t, rows)
			require.NoError(t, store.SaveDiscovery(t.Context(), inputs[0], "torrent:99:published", []byte(`{"title":"Example Later"}`), []byte(`{}`), true))
			bounded, err := store.PendingDiscoveries(t.Context(), 0, 0, 1)
			require.NoError(t, err)
			require.Len(t, bounded.Items, 1)
			require.NoError(t, store.SaveDiscovery(t.Context(), inputs[0], "torrent:100:published", []byte(`{"title":"Example Newest"}`), []byte(`{}`), true))
			tail, err := store.PendingDiscoveries(t.Context(), bounded.Items[0].ID, bounded.ThroughID, 100)
			require.NoError(t, err)
			require.Empty(t, tail.Items, "new arrivals cannot extend an existing recovery scan forever")
			fresh, err := store.PendingDiscoveries(t.Context(), 0, 0, 100)
			require.NoError(t, err)
			require.Len(t, fresh.Items, 2)
			input.Enabled = false
			_, err = store.SaveSource(t.Context(), id, input)
			require.NoError(t, err)
			require.Error(t, store.SaveCandidateMetadata(t.Context(), record.Key, inputs[0], metadata, private))
			require.NoError(t, store.InvalidateCandidate(t.Context(), record.Key, []byte(`{"state":"rejected","reason":"source_removed"}`)))
			rows, err = store.CandidateRecords(t.Context(), "", nil, 100)
			require.NoError(t, err)
			require.Equal(t, "rejected", rows[0].State)
			require.Nil(t, rows[0].NextEvaluationAt)
		})
	}
}
