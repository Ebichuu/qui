// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package qbittorrent

import (
	"slices"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
)

// ExecutionTorrent deliberately omits torrent names and private announce URLs.
// It is a transient projection, not another long-lived torrent cache.
type ExecutionTorrent struct {
	Hash          string
	HashV1        string
	HashV2        string
	SavePath      string
	DownloadPath  string
	Size          int64
	Remaining     int64
	State         qbt.TorrentState
	Downloaded    int64
	Uploaded      int64
	DownloadSpeed int64
	UploadSpeed   int64
}

type ExecutionObservation struct {
	Observation
	Torrents []ExecutionTorrent
}

// CachedExecutionObservations never creates clients or triggers a refresh. Disk
// free space and torrent progress come from one lock-consistent MainData copy.
func (cp *ClientPool) CachedExecutionObservations() []ExecutionObservation {
	cp.mu.RLock()
	clients := make([]*Client, 0, len(cp.clients))
	for _, client := range cp.clients {
		clients = append(clients, client)
	}
	cp.mu.RUnlock()
	result := make([]ExecutionObservation, 0, len(clients))
	for _, client := range clients {
		result = append(result, client.cachedExecutionObservation(time.Now()))
	}
	slices.SortFunc(result, func(a, b ExecutionObservation) int { return a.InstanceID - b.InstanceID })
	return result
}

func (c *Client) cachedExecutionObservation(now time.Time) ExecutionObservation {
	observation := c.cachedObservation(now)
	observation.Fresh = false
	result := ExecutionObservation{Observation: observation, Torrents: []ExecutionTorrent{}}
	manager := c.GetSyncManager()
	if manager == nil {
		return result
	}
	before := manager.LastSuccessfulSyncTime()
	data := manager.GetDataUnchecked()
	after := manager.LastSuccessfulSyncTime()
	if data == nil || !before.Equal(after) || manager.LastError() != nil || after.IsZero() || now.Before(after) || now.Sub(after) > 5*time.Second {
		return result
	}
	result.ObservedAt = &after
	result.Fresh = result.Healthy
	free, download, upload := data.ServerState.FreeSpaceOnDisk, data.ServerState.DlInfoSpeed, data.ServerState.UpInfoSpeed
	result.DefaultPathFreeBytes = nil
	if free >= 0 {
		result.DefaultPathFreeBytes = &free
	}
	result.DownloadSpeed = &download
	result.UploadSpeed = &upload
	result.Categories = data.Categories
	for hash, torrent := range data.Torrents {
		size := max(torrent.Size, torrent.TotalSize)
		remaining := max(torrent.AmountLeft, size-max(torrent.Completed, 0))
		if size <= 0 || torrent.AmountLeft < 0 || torrent.Completed < 0 || (torrent.HasMetadata != nil && !*torrent.HasMetadata) {
			remaining = -1
		}
		if torrent.State == qbt.TorrentStateMoving {
			remaining = size
		} else if slices.Contains([]qbt.TorrentState{qbt.TorrentStateError, qbt.TorrentStateMissingFiles, qbt.TorrentStateCheckingDl, qbt.TorrentStateCheckingUp, qbt.TorrentStateCheckingResumeData, qbt.TorrentStateUnknown}, torrent.State) {
			remaining = -1
		}
		result.Torrents = append(result.Torrents, ExecutionTorrent{Hash: hash, HashV1: torrent.InfohashV1, HashV2: torrent.InfohashV2, SavePath: torrent.SavePath, DownloadPath: torrent.DownloadPath, Size: size, Remaining: remaining, State: torrent.State, Downloaded: torrent.Downloaded, Uploaded: torrent.Uploaded, DownloadSpeed: torrent.DlSpeed, UploadSpeed: torrent.UpSpeed})
	}
	return result
}
