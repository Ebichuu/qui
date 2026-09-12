// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package reannounce

import (
	"context"
	"errors"
	"strings"

	qbt "github.com/autobrr/go-qbittorrent"
)

// DispatchReannounce applies the existing instance monitoring scope to every
// external entry point. Tasks outside that scope retain direct qB behavior.
func (s *Service) DispatchReannounce(ctx context.Context, instanceID int, hashes []string) error {
	hashes = normalizeHashes(hashes)
	if len(hashes) == 0 {
		return nil
	}
	if len(hashes) == 1 && strings.EqualFold(hashes[0], "all") {
		hashes[0] = "all"
	}
	settings, err := s.settingsStore.Get(ctx, instanceID)
	if err != nil {
		return err
	}
	client, err := s.clientPool.GetClient(ctx, instanceID)
	if err != nil {
		return err
	}
	if !settings.CanMatchTorrents() {
		return client.ReAnnounceTorrentsCtx(ctx, hashes)
	}
	options := qbt.TorrentFilterOptions{Hashes: hashes, IncludeTrackers: client.SupportsTrackerHealth()}
	if len(hashes) == 1 && strings.EqualFold(hashes[0], "all") {
		options.Hashes = nil
	}
	torrents, err := client.GetTorrentsCtx(ctx, options)
	if err != nil {
		return err
	}
	direct := []string{}
	for _, torrent := range torrents {
		if !client.SupportsTrackerHealth() || (len(settings.Trackers) > 0 && len(torrent.Trackers) == 0) {
			torrent.Trackers, err = client.GetTorrentTrackersCtx(ctx, torrent.Hash)
			if err != nil {
				return err
			}
		}
		if !s.torrentMatchesFilters(torrent, settings) {
			direct = append(direct, torrent.Hash)
			continue
		}
		// Matching tasks stay owned even while they are waiting or healthy.
		// Returning them to a proxy fallback would bypass these conditions.
		if !s.torrentMeetsCriteria(torrent, settings) || s.hasHealthyTracker(torrent.Trackers) || trackersAwaitingResponse(torrent.Trackers) {
			continue
		}
		if !s.enqueue(instanceID, strings.ToUpper(torrent.Hash), torrent.Name, s.getProblematicTrackers(torrent.Trackers)) {
			return errors.New("reannounce service is not running")
		}
	}
	if len(direct) > 0 {
		return client.ReAnnounceTorrentsCtx(ctx, direct)
	}
	return nil
}

func trackersAwaitingResponse(trackers []qbt.TorrentTracker) bool {
	for _, tracker := range trackers {
		if tracker.Status == qbt.TrackerStatusUpdating || tracker.Status == qbt.TrackerStatusNotContacted {
			return true
		}
	}
	return false
}
