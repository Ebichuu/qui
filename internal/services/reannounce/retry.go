// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package reannounce

import (
	"context"
	"errors"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/autobrr/qui/internal/qbittorrent"
)

var (
	errTrackerUnconfirmed = errors.New("target tracker acceptance remains unconfirmed")
	errTrackerChanged     = errors.New("target tracker inventory changed or is unavailable")
	errTrackerPartial     = errors.New("partial tracker acceptance; torrent-wide retry deferred")
)

type reannounceClient interface {
	GetTorrentTrackersCtx(context.Context, string) ([]qbt.TorrentTracker, error)
	ReAnnounceTorrentsCtx(context.Context, []string) error
}

// retryReannounce confirms the original exact tracker identities, never just
// HTTP acceptance or a different working tracker. URLs stay in memory only.
// qB's reannounce operation affects the torrent, so partial acceptance stops
// further requests until shared site constraints can authorize them.
func retryReannounce(ctx context.Context, client reannounceClient, hash string, initial []qbt.TorrentTracker, interval time.Duration, attempts int) error {
	targets := make(map[string]struct{})
	for _, tracker := range initial {
		if tracker.Status != qbt.TrackerStatusDisabled {
			if tracker.Url == "" {
				return errTrackerChanged
			}
			targets[tracker.Url] = struct{}{}
		}
	}
	if len(targets) == 0 || interval <= 0 || attempts < 1 {
		return errTrackerUnconfirmed
	}
	for round := 0; round <= attempts; round++ {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		trackers, err := client.GetTorrentTrackersCtx(ctx, hash)
		if err != nil {
			return err
		}
		seen := make(map[string]bool)
		accepted, waiting := 0, false
		for _, tracker := range trackers {
			healthy := tracker.Status == qbt.TrackerStatusOK && !qbittorrent.TrackerMessageMatchesUnregistered(tracker.Message)
			if _, target := targets[tracker.Url]; !target {
				if tracker.Status != qbt.TrackerStatusDisabled {
					return errTrackerChanged
				}
				continue
			}
			if seen[tracker.Url] || tracker.Status == qbt.TrackerStatusDisabled {
				return errTrackerChanged
			}
			seen[tracker.Url] = true
			if healthy {
				accepted++
			}
			waiting = waiting || tracker.Status == qbt.TrackerStatusUpdating || tracker.Status == qbt.TrackerStatusNotContacted
		}
		if len(seen) != len(targets) {
			return errTrackerChanged
		}
		if accepted == len(targets) {
			return nil
		}
		if accepted > 0 {
			return errTrackerPartial
		}
		if round == attempts {
			return errTrackerUnconfirmed
		}
		if waiting {
			continue
		}
		if err := client.ReAnnounceTorrentsCtx(ctx, []string{hash}); err != nil {
			return err
		}
	}
	return errTrackerUnconfirmed
}
