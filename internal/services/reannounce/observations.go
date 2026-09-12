// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package reannounce

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/qbittorrent"
)

type observedReannounceClient struct {
	*qbittorrent.Client
	service      *Service
	instanceID   int
	addedOn      int64
	lastTrackers []qbt.TorrentTracker
	lastUploaded int64
	policyOnly   bool
	fresh        bool
}

func (c *observedReannounceClient) ReAnnounceTorrentsCtx(ctx context.Context, hashes []string) error {
	if len(hashes) != 1 {
		return errors.New("tracker job requires one task")
	}
	c.fresh = true
	if _, err := c.GetTorrentTrackersCtx(ctx, hashes[0]); err != nil {
		return err
	}
	torrent, _, err := c.torrent(ctx, hashes[0])
	if err != nil {
		return err
	}
	if torrent.AddedOn != c.addedOn || torrent.Uploaded < c.lastUploaded {
		return errTrackerChanged
	}
	settings, err := c.service.settingsStore.Get(ctx, c.instanceID)
	if err != nil {
		return err
	}
	if !c.policyOnly && !settings.CanMatchTorrents() {
		return errReannounceDeferred
	}
	// Refresh filters as well as timing after a setting edit during a job.
	torrent.Trackers = c.lastTrackers
	if !c.policyOnly && (!c.service.torrentMeetsCriteria(torrent, settings) || c.service.hasHealthyTracker(c.lastTrackers) || trackersAwaitingResponse(c.lastTrackers)) {
		return errReannounceDeferred
	}
	policies, err := c.service.settingsStore.TrackerPolicies(ctx, c.instanceID)
	if err != nil {
		return err
	}
	constraint, err := c.service.trackerConstraints(ctx, c.instanceID, torrent, c.lastTrackers, policies, false)
	if err != nil {
		return err
	}
	if (c.policyOnly && !constraint.Managed) || (constraint.Managed && !constraint.ReannounceAllowed) {
		return errReannounceDeferred
	}
	interval := constraint.IntervalSeconds
	if !c.policyOnly {
		interval = max(settings.ReannounceIntervalSeconds, interval)
	}
	allowed, err := c.service.settingsStore.BeginReannounce(ctx, c.instanceID, hashes[0], time.Now(), time.Duration(interval)*time.Second)
	if err != nil {
		return err
	}
	if !allowed {
		return errReannounceDeferred
	}
	return c.ReannounceOnce(ctx, hashes)
}

func trackerObservations(trackers []qbt.TorrentTracker) []models.TrackerObservation {
	rows := []models.TrackerObservation{}
	for _, tracker := range trackers {
		u, err := url.Parse(tracker.Url)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "udp") {
			continue
		}
		digest := sha256.Sum256([]byte(tracker.Url))
		row := models.TrackerObservation{Key: hex.EncodeToString(digest[:]), Host: strings.ToLower(u.Hostname()), State: "unknown"}
		if tracker.Message != "" {
			message := sha256.Sum256([]byte(tracker.Message))
			row.MessageDigest = hex.EncodeToString(message[:])
		}
		switch tracker.Status {
		case qbt.TrackerStatusDisabled:
			row.State = "disabled"
		case qbt.TrackerStatusUpdating:
			row.State = "updating"
		case qbt.TrackerStatusNotContacted:
			row.State = "not_contacted"
		case qbt.TrackerStatusOK:
			row.State = "reported_working"
			if qbittorrent.TrackerMessageMatchesUnregistered(tracker.Message) {
				row.State = "error_unknown"
			}
		case qbt.TrackerStatusNotWorking, qbt.TrackerStatusTrackerError:
			row.State = "error_unknown"
		case qbt.TrackerStatusUnreachable:
			row.State = "unreachable"
		}
		// The legacy display classifier includes broad words such as "unknown".
		// Persist a specific cause only when the message actually states it.
		message := strings.ToLower(tracker.Message)
		if tracker.Status != qbt.TrackerStatusDisabled && (strings.Contains(message, "torrent not registered") || strings.Contains(message, "infohash not found")) {
			row.State = "unregistered"
		}
		rows = append(rows, row)
	}
	return rows
}

func (c *observedReannounceClient) GetTorrentTrackersCtx(ctx context.Context, hash string) ([]qbt.TorrentTracker, error) {
	before, _, err := c.torrent(ctx, hash)
	if err != nil {
		return nil, err
	}
	if c.addedOn != 0 && (c.addedOn != before.AddedOn || before.Uploaded < c.lastUploaded) {
		return nil, errTrackerChanged
	}
	c.addedOn = before.AddedOn
	trackers, err := c.Client.GetTorrentTrackersCtx(ctx, hash)
	if err != nil {
		return nil, err
	}
	observed := time.Now().UTC()
	after, at, err := c.torrent(ctx, hash)
	if err != nil {
		return nil, err
	}
	if after.AddedOn != before.AddedOn || after.Uploaded < before.Uploaded {
		return nil, errTrackerChanged
	}
	if err := c.service.settingsStore.SaveObservation(ctx, models.ReannounceObservation{InstanceID: c.instanceID, Hash: hash, AddedOn: after.AddedOn, ObservedAt: observed, LocalObservedAt: at, LocalUploaded: after.Uploaded, Trackers: trackerObservations(trackers)}); err != nil {
		return nil, err
	}
	c.lastTrackers = trackers
	c.lastUploaded = after.Uploaded
	return trackers, nil
}

func (c *observedReannounceClient) torrent(ctx context.Context, hash string) (qbt.Torrent, time.Time, error) {
	rows, at, err := c.service.syncManager.GetObservedTorrents(ctx, c.instanceID)
	if err != nil || c.fresh {
		// A monitoring-only instance may not have a fast MainData consumer.
		// Read this one task on demand; do not start another full-cache poller.
		rows, err = c.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{Hashes: []string{hash}})
		at = time.Now().UTC()
		if err != nil {
			return qbt.Torrent{}, at, err
		}
	}
	for _, row := range rows {
		if strings.EqualFold(row.Hash, hash) && row.AddedOn > 0 && row.Uploaded >= 0 {
			return row, at, nil
		}
	}
	return qbt.Torrent{}, at, errors.New("tracker observation task generation unavailable")
}
