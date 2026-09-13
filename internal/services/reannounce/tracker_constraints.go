// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package reannounce

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/qbittorrent"
)

type TrackerConstraintResult struct {
	Managed           bool      `json:"managed"`
	ReannounceAllowed bool      `json:"reannounceAllowed"`
	DeleteAllowed     bool      `json:"deleteAllowed"`
	IntervalSeconds   int       `json:"intervalSeconds"`
	NotBefore         time.Time `json:"notBefore"`
	Reason            string    `json:"reason"`
}

func trackerDigest(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

// Every enabled tracker affected by qB's torrent-wide request must be covered
// when any exact account binding applies. Working qB state is not site accounting.
func (s *Service) trackerConstraints(ctx context.Context, instanceID int, torrent qbt.Torrent, trackers []qbt.TorrentTracker, policies []models.ReannounceTrackerPolicy, requireCoverage bool) (TrackerConstraintResult, error) {
	result := TrackerConstraintResult{Managed: requireCoverage, ReannounceAllowed: true, DeleteAllowed: true, Reason: "unmanaged"}
	byKey := make(map[string]models.ReannounceTrackerPolicy, len(policies))
	present := make(map[string]bool, len(trackers))
	for _, policy := range policies {
		byKey[policy.TrackerKey] = policy
	}
	for _, tracker := range trackers {
		present[trackerDigest(tracker.Url)] = true
		if _, found := byKey[trackerDigest(tracker.Url)]; found {
			result.Managed = true
		}
		if u, err := url.Parse(tracker.Url); err == nil {
			for _, policy := range policies {
				if strings.EqualFold(u.Hostname(), policy.TrackerHost) {
					result.Managed = true
				}
			}
		}
	}
	recorded, err := s.settingsStore.RecordedTrackerKeys(ctx, instanceID, torrent.Hash, torrent.AddedOn)
	if err != nil {
		return result, err
	}
	for _, key := range recorded {
		if _, exists := byKey[key]; exists && !present[key] {
			result.Managed, result.ReannounceAllowed, result.DeleteAllowed, result.Reason = true, false, false, "tracker_removed"
			return result, nil
		}
	}
	if len(policies) > 0 && len(trackers) == 0 {
		result.Managed = true
	}
	if !result.Managed {
		return result, nil
	}
	result.Reason = "allowed"
	if torrent.Hash == "" || torrent.AddedOn <= 0 || len(trackers) == 0 {
		result.ReannounceAllowed, result.DeleteAllowed, result.Reason = false, false, "unknown_task_or_trackers"
		return result, nil
	}
	now := time.Now().UTC()
	last, next, err := s.settingsStore.ReannounceTiming(ctx, instanceID, torrent.Hash)
	if err != nil {
		return result, err
	}
	if next > 0 {
		result.NotBefore = time.Unix(0, next)
	}
	covered := 0
	for _, tracker := range trackers {
		u, err := url.Parse(tracker.Url)
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "udp") {
			if tracker.Status != qbt.TrackerStatusDisabled {
				result.ReannounceAllowed, result.DeleteAllowed, result.Reason = false, false, "unknown_tracker"
			}
			continue
		}
		key := trackerDigest(tracker.Url)
		policy, found := byKey[key]
		if !found || policy.TrackerHost != strings.ToLower(u.Hostname()) {
			result.ReannounceAllowed, result.DeleteAllowed, result.Reason = false, false, "uncovered_tracker"
			continue
		}
		covered++
		result.IntervalSeconds = max(result.IntervalSeconds, policy.IntervalSeconds)
		message := trackerDigest(tracker.Message)
		delay := time.Duration(0)
		if policy.WaitMessageDigest != "" && message == policy.WaitMessageDigest {
			delay = time.Duration(policy.WaitSeconds) * time.Second
		}
		token := message + ":" + strconv.FormatInt(last, 10)
		deadline, err := s.settingsStore.ObserveTrackerWait(ctx, instanceID, torrent.Hash, torrent.AddedOn, key, token, now, delay)
		if err != nil {
			return result, err
		}
		if deadline.After(result.NotBefore) {
			result.NotBefore = deadline
		}
		if tracker.Status == qbt.TrackerStatusUpdating || tracker.Status == qbt.TrackerStatusNotContacted || tracker.Status == qbt.TrackerStatusDisabled {
			result.ReannounceAllowed = false
			result.Reason = "tracker_waiting_or_disabled"
		}
		if tracker.Status == qbt.TrackerStatusOK && !qbittorrent.TrackerMessageMatchesUnregistered(tracker.Message) {
			result.ReannounceAllowed = false
			result.Reason = "tracker_already_working"
		}
		if policy.DeleteProtection != "reported_working" || tracker.Status != qbt.TrackerStatusOK || qbittorrent.TrackerMessageMatchesUnregistered(tracker.Message) {
			result.DeleteAllowed = false
		}
	}
	if covered == 0 {
		result.ReannounceAllowed, result.DeleteAllowed, result.Reason = false, false, "uncovered_tracker"
		return result, nil
	}
	if last > 0 {
		deadline := time.Unix(0, last).Add(time.Duration(result.IntervalSeconds) * time.Second)
		if deadline.After(result.NotBefore) {
			result.NotBefore = deadline
		}
	}
	if now.Before(result.NotBefore) {
		result.ReannounceAllowed, result.DeleteAllowed, result.Reason = false, false, "waiting"
	}
	if result.ReannounceAllowed && !result.DeleteAllowed {
		result.Reason = "delete_evidence_missing"
	}
	return result, nil
}

func (s *Service) CheckTrackerDeletion(ctx context.Context, instanceID int, hash string, addedOn int64, requireCoverage bool) (TrackerConstraintResult, error) {
	policies, err := s.settingsStore.TrackerPolicies(ctx, instanceID)
	if err != nil {
		return TrackerConstraintResult{}, err
	}
	if len(policies) == 0 && !requireCoverage {
		return TrackerConstraintResult{DeleteAllowed: true, Reason: "unmanaged"}, nil
	}
	client, err := s.clientPool.GetClient(ctx, instanceID)
	if err != nil {
		return TrackerConstraintResult{}, err
	}
	observed := &observedReannounceClient{Client: client, service: s, instanceID: instanceID, addedOn: addedOn, fresh: true}
	trackers, err := observed.GetTorrentTrackersCtx(ctx, hash)
	if err != nil {
		return TrackerConstraintResult{}, err
	}
	torrent, _, err := observed.torrent(ctx, hash)
	if err != nil {
		return TrackerConstraintResult{}, err
	}
	if torrent.AddedOn != addedOn {
		return TrackerConstraintResult{}, errTrackerChanged
	}
	policies, err = s.settingsStore.TrackerPolicies(ctx, instanceID)
	if err != nil {
		return TrackerConstraintResult{}, err
	}
	return s.trackerConstraints(ctx, instanceID, torrent, trackers, policies, requireCoverage)
}

func (s *Service) GuardAutomaticDelete(ctx context.Context, instanceID int, candidates []models.DeleteIdentity) error {
	for _, item := range candidates {
		result, err := s.CheckTrackerDeletion(ctx, instanceID, item.Hash, item.AddedOn, false)
		if err != nil {
			return err
		}
		if !result.DeleteAllowed {
			return fmt.Errorf("automatic deletion blocked by tracker protection: %s", result.Reason)
		}
	}
	return nil
}

// GuardReclaimDelete requires coverage for every tracker before official reclaim.
func (s *Service) GuardReclaimDelete(ctx context.Context, instanceID int, candidates []models.DeleteIdentity) error {
	for _, item := range candidates {
		result, err := s.CheckTrackerDeletion(ctx, instanceID, item.Hash, item.AddedOn, true)
		if err != nil {
			return err
		}
		if !result.DeleteAllowed {
			return fmt.Errorf("official reclaim blocked by tracker protection: %s", result.Reason)
		}
	}
	return nil
}
