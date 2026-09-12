// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package qbittorrent

import (
	"context"
	"errors"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
)

// GetObservedTorrents returns one real sync sample without optimistic UI changes.
// Repeated reads of a sample keep its timestamp and cannot mature observations.
func (sm *SyncManager) GetObservedTorrents(ctx context.Context, instanceID int) ([]qbt.Torrent, time.Time, error) {
	manager, err := sm.GetQBittorrentSyncManager(ctx, instanceID)
	if err != nil {
		return nil, time.Time{}, err
	}
	before := manager.LastSuccessfulSyncTime()
	data := manager.GetDataUnchecked()
	after := manager.LastSuccessfulSyncTime()
	now := time.Now()
	if data == nil || !before.Equal(after) || manager.LastError() != nil || after.IsZero() || now.Before(after) || now.Sub(after) > 5*time.Second {
		return nil, time.Time{}, errors.New("fresh torrent observation unavailable")
	}
	torrents := make([]qbt.Torrent, 0, len(data.Torrents))
	for hash, torrent := range data.Torrents {
		torrent.Hash = hash
		torrents = append(torrents, torrent)
	}
	return torrents, after, nil
}
