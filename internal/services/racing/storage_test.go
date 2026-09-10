// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/qbittorrent"
)

func TestSharedStorageObservationsDoNotSumOrBorrowOtherDisks(t *testing.T) {
	config := &models.RacingConfiguration{
		StoragePools: []models.RacingStoragePool{{ID: 1}, {ID: 2}},
		PathMappings: []models.RacingPathMapping{
			{InstanceID: 7, StoragePoolID: 1, Path: "/data"},
			{InstanceID: 7, StoragePoolID: 2, Path: "/data/other-disk"},
			{InstanceID: 9, StoragePoolID: 1, Path: "/shared"},
		},
	}
	for _, tc := range []struct {
		path  string
		pool  int
		known bool
	}{
		{"/data/downloads", 1, true}, {"/data/other-disk/jobs", 2, true}, {"/data-other", 0, false}, {"relative", 0, false},
	} {
		pool, known := ResolveStoragePool(config.PathMappings, 7, tc.path)
		require.Equal(t, tc.pool, pool)
		require.Equal(t, tc.known, known)
	}
	now, earlier := time.Now(), time.Now().Add(-time.Second)
	a, b := int64(1000), int64(900)
	instances := []qbittorrent.Observation{
		{InstanceID: 7, Fresh: true, PreferencesFresh: true, SavePath: "/data/default", DefaultPathFreeBytes: &a, ObservedAt: &now, PreferencesObservedAt: &earlier},
		{InstanceID: 9, Fresh: true, PreferencesFresh: true, SavePath: "/shared/default", DefaultPathFreeBytes: &b, ObservedAt: &now, PreferencesObservedAt: &earlier},
	}
	observed := observeStorage(config, instances)
	require.Equal(t, int64(900), *observed[0].FreeBytes)
	require.Nil(t, observed[1].FreeBytes, "another disk must remain unknown")
	instances[0].Fresh = false
	instances[1].PreferencesFresh = false
	require.Nil(t, observeStorage(config, instances)[0].FreeBytes)
	instances[1].PreferencesFresh = true
	instances[1].PreferencesObservedAt = &now
	instances[1].ObservedAt = &earlier
	require.Nil(t, observeStorage(config, instances)[0].FreeBytes, "path changed after free-space observation")
}
