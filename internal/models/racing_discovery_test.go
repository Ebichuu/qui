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

func TestRacingDiscoveryPersistence(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var db *database.DB
			if engine == "sqlite" {
				db = testdb.NewMigratedSQLite(t, "racing-discovery")
			} else {
				db = testdb.NewMigratedPostgres(t, "racing-discovery")
			}
			store, err := models.NewRacingStore(db, bytes.Repeat([]byte{4}, 32))
			require.NoError(t, err)
			cookie := "sid=synthetic-private-cookie"
			siteInput := models.RacingSiteInput{Name: "Synthetic site", Enabled: true, BaseURL: "https://tracker.invalid", RequestIntervalSeconds: 1, Credential: &cookie}
			site, err := store.SaveSite(t.Context(), 0, siteInput)
			require.NoError(t, err)
			endpoint := "https://tracker.invalid/feed?key=synthetic-private-key"
			sourceInput := models.RacingSourceInput{SiteID: site, Name: "Synthetic RSS", Kind: "rss", Adapter: "chd", Enabled: true, IntervalSeconds: 1, InitialLookbackSeconds: 60, URL: &endpoint}
			id, err := store.SaveSource(t.Context(), 0, sourceInput)
			require.NoError(t, err)
			runtime, err := store.RuntimeSources(t.Context())
			require.NoError(t, err)
			require.Len(t, runtime, 1)
			require.Equal(t, endpoint, runtime[0].URL)
			require.Equal(t, cookie, runtime[0].Cookie)
			encoded, err := json.Marshal(runtime)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "synthetic-private")
			baseline, err := store.SourceBaseline(t.Context(), id, 60)
			require.NoError(t, err)
			require.WithinDuration(t, time.Now().Add(-time.Minute), baseline, 2*time.Second)
			restarted, err := models.NewRacingStore(db, bytes.Repeat([]byte{4}, 32))
			require.NoError(t, err)
			again, err := restarted.SourceBaseline(t.Context(), id, 0)
			require.NoError(t, err)
			require.Equal(t, baseline, again)
			public := []byte(`{"torrentId":"42","title":"Example Aurora","free":{"value":"unknown"}}`)
			private := []byte(`{"downloadUrl":"https://tracker.invalid/download?id=42&key=synthetic-private-key"}`)
			require.NoError(t, store.SaveDiscovery(t.Context(), runtime[0], "torrent:42:published", public, private, true))
			rows, err := store.Discoveries(t.Context(), 100)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			first := rows[0]
			require.True(t, first.Eligible)
			require.Equal(t, site, first.SiteID)
			require.NoError(t, store.SaveDiscovery(t.Context(), runtime[0], "torrent:42:published", public, private, true))
			rows, err = store.Discoveries(t.Context(), 100)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, first.ID, rows[0].ID)
			require.Equal(t, first.FirstSeenAt, rows[0].FirstSeenAt)
			require.Equal(t, int64(1), rows[0].Revision)
			public = []byte(`{"torrentId":"42","title":"Example Aurora","free":{"value":"true"}}`)
			require.NoError(t, store.SaveDiscovery(t.Context(), runtime[0], "torrent:42:published", public, private, true))
			rows, err = store.Discoveries(t.Context(), 100)
			require.NoError(t, err)
			require.Equal(t, int64(2), rows[0].Revision)
			var ciphertext string
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT private_ciphertext FROM racing_discoveries WHERE id=?`, first.ID).Scan(&ciphertext))
			require.NotContains(t, ciphertext, "synthetic-private")
			plain, err := restarted.PrivateDiscovery(t.Context(), first.ID)
			require.NoError(t, err)
			require.JSONEq(t, string(private), string(plain))
			encoded, err = json.Marshal(rows)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "synthetic-private")
			require.NoError(t, store.CompleteSourceScan(t.Context(), runtime[0]))
			var completed string
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT last_success_at FROM racing_source_cursors WHERE source_id=?`, id).Scan(&completed))
			require.NotEmpty(t, completed)
			// A second source retains its own arrival for the same site's event.
			_, err = store.SaveSource(t.Context(), 0, sourceInput)
			require.NoError(t, err)
			runtime, err = store.RuntimeSources(t.Context())
			require.NoError(t, err)
			require.Len(t, runtime, 2)
			require.NoError(t, store.SaveDiscovery(t.Context(), runtime[1], "torrent:42:published", public, private, true))
			rows, err = store.Discoveries(t.Context(), 100)
			require.NoError(t, err)
			require.Len(t, rows, 2)
			// Saving or disabling configuration fences late responses from old workers.
			sourceInput.Enabled = false
			_, err = store.SaveSource(t.Context(), id, sourceInput)
			require.NoError(t, err)
			require.Error(t, store.SaveDiscovery(t.Context(), runtime[0], "torrent:99:published", public, private, true))
			require.NoError(t, store.Delete(t.Context(), "sources", id))
			rows, err = store.Discoveries(t.Context(), 100)
			require.NoError(t, err)
			require.Len(t, rows, 1)
		})
	}
}
