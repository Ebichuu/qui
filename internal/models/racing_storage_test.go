// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/database"
	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestRacingStorageLifecycle(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var db *database.DB
			if engine == "sqlite" {
				db = testdb.NewMigratedSQLite(t, "racing-storage")
			} else {
				db = testdb.NewMigratedPostgres(t, "racing-storage")
			}
			key := bytes.Repeat([]byte{7}, 32)
			store, err := models.NewRacingStore(db, key)
			require.NoError(t, err)
			instances, err := models.NewInstanceStore(db, key)
			require.NoError(t, err)
			instance, err := instances.Create(t.Context(), "Synthetic storage member", "http://127.0.0.1:1", "test", "test", nil, nil, false, nil)
			require.NoError(t, err)
			pool, err := store.SaveStoragePool(t.Context(), 0, models.RacingStoragePoolInput{Name: "Disk A"})
			require.NoError(t, err)
			mapping, err := store.SavePathMapping(t.Context(), 0, models.RacingPathMappingInput{InstanceID: instance.ID, StoragePoolID: pool, Path: "/data/"})
			require.NoError(t, err)
			_, err = store.SavePathMapping(t.Context(), 0, models.RacingPathMappingInput{InstanceID: instance.ID, StoragePoolID: pool, Path: "/data"})
			require.ErrorIs(t, err, models.ErrRacingInvalid)
			require.ErrorIs(t, store.Delete(t.Context(), "storage-pools", pool), models.ErrRacingReferenced)
			_, err = store.SavePathMapping(t.Context(), mapping, models.RacingPathMappingInput{InstanceID: instance.ID, StoragePoolID: 999999, Path: "/other"})
			require.ErrorIs(t, err, models.ErrRacingInvalid)
			_, err = store.SaveStoragePool(t.Context(), pool, models.RacingStoragePoolInput{Name: "Renamed disk"})
			require.NoError(t, err)
			restarted, err := models.NewRacingStore(db, key)
			require.NoError(t, err)
			config, err := restarted.Configuration(t.Context())
			require.NoError(t, err)
			require.Equal(t, pool, config.StoragePools[0].ID)
			require.Equal(t, "Renamed disk", config.StoragePools[0].Name)
			require.Equal(t, "/data", config.PathMappings[0].Path, "failed update rolls back")
			require.NoError(t, store.Delete(t.Context(), "path-mappings", mapping))
			require.NoError(t, store.Delete(t.Context(), "storage-pools", pool))
		})
	}
}

func TestNormalizeRacingPath(t *testing.T) {
	for _, tc := range []struct{ input, expected string }{
		{"/data/jobs/", "/data/jobs"}, {`C:\Data\Jobs\`, "c:/data/jobs"}, {`\\server\share\Jobs`, "//server/share/jobs"}, {"C:/", "c:/"}, {"/", "/"},
	} {
		got, err := models.NormalizeRacingPath(tc.input)
		require.NoError(t, err)
		require.Equal(t, tc.expected, got)
	}
	for _, raw := range []string{"", "relative/path", "C:relative", "/data/../other", "C:/data/../other", "//server", "/data\x00"} {
		_, err := models.NormalizeRacingPath(raw)
		require.ErrorIs(t, err, models.ErrRacingInvalid)
	}
}
