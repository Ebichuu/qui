// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"strings"
	"time"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/qbittorrent"
)

type StorageObservation struct {
	StoragePoolID int        `json:"storagePoolId"`
	FreeBytes     *int64     `json:"freeBytes,omitempty"`
	ObservedAt    *time.Time `json:"observedAt,omitempty"`
}

// ResolveStoragePool uses the most specific configured path, allowing a nested
// mount to identify a different disk. Group membership never enters this lookup.
func ResolveStoragePool(mappings []models.RacingPathMapping, instanceID int, actualPath string) (int, bool) {
	actual, err := models.NormalizeRacingPath(actualPath)
	if err != nil {
		return 0, false
	}
	matched, poolID := -1, 0
	for _, mapping := range mappings {
		if mapping.InstanceID != instanceID {
			continue
		}
		root := mapping.Path
		if actual == root || strings.HasPrefix(actual, strings.TrimSuffix(root, "/")+"/") {
			if len(root) > matched {
				matched, poolID = len(root), mapping.StoragePoolID
			}
		}
	}
	return poolID, matched >= 0
}

func observeStorage(config *models.RacingConfiguration, instances []qbittorrent.Observation) []StorageObservation {
	result := make([]StorageObservation, 0, len(config.StoragePools))
	indexes := make(map[int]int, len(config.StoragePools))
	for _, pool := range config.StoragePools {
		indexes[pool.ID] = len(result)
		result = append(result, StorageObservation{StoragePoolID: pool.ID})
	}
	for _, instance := range instances {
		// qB's free-space value only measures the current default save path. A
		// fresh MainData sample must follow the latest successful path observation.
		if !instance.Fresh || !instance.PreferencesFresh || instance.DefaultPathFreeBytes == nil || instance.ObservedAt == nil || instance.PreferencesObservedAt == nil || instance.ObservedAt.Before(*instance.PreferencesObservedAt) {
			continue
		}
		pool, found := ResolveStoragePool(config.PathMappings, instance.InstanceID, instance.SavePath)
		index, exists := indexes[pool]
		if !found || !exists {
			continue
		}
		item := &result[index]
		// Multiple clients can report the same disk. Never sum the observations;
		// the smallest current value is the conservative shared capacity estimate.
		if item.FreeBytes == nil || *instance.DefaultPathFreeBytes < *item.FreeBytes {
			free, at := *instance.DefaultPathFreeBytes, *instance.ObservedAt
			item.FreeBytes, item.ObservedAt = &free, &at
		}
	}
	return result
}
