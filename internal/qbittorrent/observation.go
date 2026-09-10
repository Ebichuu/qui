// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package qbittorrent

import (
	"context"
	"slices"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
)

// Observation is a small projection of the existing shared caches. Missing or
// stale values never imply an idle downloader or known target disk capacity.
type Observation struct {
	InstanceID            int                     `json:"instanceId"`
	ObservedAt            *time.Time              `json:"observedAt,omitempty"`
	Fresh                 bool                    `json:"fresh"`
	Healthy               bool                    `json:"healthy"`
	DownloadSpeed         *int64                  `json:"downloadSpeed,omitempty"`
	UploadSpeed           *int64                  `json:"uploadSpeed,omitempty"`
	DefaultPathFreeBytes  *int64                  `json:"defaultPathFreeBytes,omitempty"`
	Version               string                  `json:"version"`
	WebAPIVersion         string                  `json:"webAPIVersion"`
	MetadataObservedAt    *time.Time              `json:"metadataObservedAt,omitempty"`
	PreferencesObservedAt *time.Time              `json:"preferencesObservedAt,omitempty"`
	PreferencesFresh      bool                    `json:"preferencesFresh"`
	SavePath              string                  `json:"savePath"`
	TempPath              string                  `json:"tempPath"`
	TempPathEnabled       bool                    `json:"tempPathEnabled"`
	Categories            map[string]qbt.Category `json:"categories"`
}

// CachedObservations performs no database lookup, client creation or network I/O.
func (cp *ClientPool) CachedObservations() []Observation {
	cp.mu.RLock()
	clients := make([]*Client, 0, len(cp.clients))
	for _, client := range cp.clients {
		clients = append(clients, client)
	}
	cp.mu.RUnlock()
	result := make([]Observation, 0, len(clients))
	now := time.Now()
	for _, client := range clients {
		result = append(result, client.cachedObservation(now))
	}
	slices.SortFunc(result, func(a, b Observation) int { return a.InstanceID - b.InstanceID })
	return result
}

func (c *Client) cachedObservation(now time.Time) Observation {
	result := Observation{InstanceID: c.instanceID, Healthy: c.IsHealthy(), WebAPIVersion: c.GetWebAPIVersion(), Categories: map[string]qbt.Category{}}
	c.serverStateMu.RLock()
	if c.lastServerState != nil && !c.serverStateObservedAt.IsZero() {
		observed := c.serverStateObservedAt
		result.ObservedAt = &observed
		result.Fresh = result.Healthy && !observed.Before(c.serverStateErrorAt) && now.Sub(observed) >= 0 && now.Sub(observed) <= 5*time.Second
		download, upload := c.lastServerState.DlInfoSpeed, c.lastServerState.UpInfoSpeed
		result.DownloadSpeed, result.UploadSpeed = &download, &upload
		if free := c.lastServerState.FreeSpaceOnDisk; free >= 0 {
			result.DefaultPathFreeBytes = &free
		}
	}
	c.serverStateMu.RUnlock()
	c.preferencesMu.RLock()
	if c.preferencesCache != nil {
		observed := c.preferencesFetchedAt
		result.PreferencesObservedAt = &observed
		result.PreferencesFresh = now.Sub(observed) >= 0 && now.Sub(observed) <= appPreferencesCacheTTL
		result.SavePath, result.TempPath, result.TempPathEnabled = c.preferencesCache.SavePath, c.preferencesCache.TempPath, c.preferencesCache.TempPathEnabled
	}
	c.preferencesMu.RUnlock()
	c.appInfoMu.RLock()
	if c.appInfoCache != nil {
		observed := c.appInfoFetchedAt
		result.MetadataObservedAt = &observed
		result.Version, result.WebAPIVersion = c.appInfoCache.Version, c.appInfoCache.WebAPIVersion
	}
	c.appInfoMu.RUnlock()
	if manager := c.GetSyncManager(); manager != nil {
		result.Categories = manager.GetCategoriesUnchecked()
	}
	return result
}

// RefreshMetadataInBackground shares the existing preferences/app caches. Slow
// metadata requests must not hold up MainData sync or another instance. Each
// client has at most one refresh, with a bounded retry interval on failures.
func (c *Client) RefreshMetadataInBackground(parent context.Context) {
	c.metadataRefreshMu.Lock()
	if c.metadataRefreshing || time.Since(c.metadataAttemptAt) < 20*time.Second {
		c.metadataRefreshMu.Unlock()
		return
	}
	c.metadataRefreshing, c.metadataAttemptAt = true, time.Now()
	c.metadataRefreshMu.Unlock()
	go func() {
		defer func() { c.metadataRefreshMu.Lock(); c.metadataRefreshing = false; c.metadataRefreshMu.Unlock() }()
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		// Refresh before the 30-second freshness window expires.
		_, _ = c.refreshAppPreferences(ctx)
		if ctx.Err() != nil {
			return
		}
		_, _ = c.GetAppInfo(ctx)
	}()
}
