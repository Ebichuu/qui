// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

type ReannounceTrackerPolicy struct {
	TrackerKey        string `json:"trackerKey"`
	SiteID            int    `json:"siteId"`
	TrackerHost       string `json:"trackerHost"`
	IntervalSeconds   int    `json:"intervalSeconds"`
	WaitMessageDigest string `json:"waitMessageDigest"`
	WaitSeconds       int    `json:"waitSeconds"`
	DeleteProtection  string `json:"deleteProtection"`
}

var ErrTrackerPolicyInvalid = errors.New("invalid tracker policy")

func (s *InstanceReannounceStore) RecordedTrackerKeys(ctx context.Context, instanceID int, hash string, addedOn int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tracker_key FROM reannounce_tracker_waits WHERE instance_id=? AND torrent_hash=? AND added_on=?`, instanceID, strings.ToLower(hash), addedOn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *InstanceReannounceStore) SaveTrackerPolicy(ctx context.Context, instanceID int, item ReannounceTrackerPolicy) error {
	key, err := hex.DecodeString(item.TrackerKey)
	if err != nil || len(key) != 32 || item.SiteID <= 0 || item.TrackerHost == "" || item.IntervalSeconds <= 0 || item.IntervalSeconds > 2592000 || item.WaitSeconds < 0 || item.WaitSeconds > 2592000 || len(item.WaitMessageDigest) > 64 || (strings.TrimSpace(item.WaitMessageDigest) == "") != (item.WaitSeconds == 0) || (item.DeleteProtection != "blocked" && item.DeleteProtection != "reported_working" && item.DeleteProtection != "accounted") {
		return ErrTrackerPolicyInvalid
	}
	if item.WaitMessageDigest != "" {
		digest, err := hex.DecodeString(item.WaitMessageDigest)
		if err != nil || len(digest) != 32 {
			return ErrTrackerPolicyInvalid
		}
	}
	item.WaitMessageDigest = strings.ToLower(item.WaitMessageDigest)
	item.TrackerKey = strings.ToLower(item.TrackerKey)
	item.TrackerHost = strings.ToLower(strings.TrimSpace(item.TrackerHost))
	var raw string
	var enabled bool
	if err := s.db.QueryRowContext(ctx, `SELECT tracker_hosts,enabled FROM racing_sites WHERE id=?`, item.SiteID).Scan(&raw, &enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTrackerPolicyInvalid
		}
		return err
	}
	if !enabled {
		return ErrTrackerPolicyInvalid
	}
	var hosts []string
	if err := json.Unmarshal([]byte(raw), &hosts); err != nil {
		return err
	}
	if !slices.Contains(hosts, item.TrackerHost) {
		return ErrTrackerPolicyInvalid
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO reannounce_tracker_policies(instance_id,tracker_key,site_id,tracker_host,interval_seconds,wait_message_digest,wait_seconds,delete_protection) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,tracker_key) DO UPDATE SET site_id=excluded.site_id,tracker_host=excluded.tracker_host,interval_seconds=excluded.interval_seconds,wait_message_digest=excluded.wait_message_digest,wait_seconds=excluded.wait_seconds,delete_protection=excluded.delete_protection`, instanceID, item.TrackerKey, item.SiteID, item.TrackerHost, item.IntervalSeconds, item.WaitMessageDigest, item.WaitSeconds, item.DeleteProtection)
	return err
}

func (s *InstanceReannounceStore) TrackerPolicies(ctx context.Context, instanceID int) ([]ReannounceTrackerPolicy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.tracker_key,p.site_id,p.tracker_host,p.interval_seconds,p.wait_message_digest,p.wait_seconds,p.delete_protection,s.tracker_hosts,s.enabled FROM reannounce_tracker_policies p JOIN racing_sites s ON s.id=p.site_id WHERE p.instance_id=? ORDER BY p.tracker_key`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReannounceTrackerPolicy{}
	for rows.Next() {
		var item ReannounceTrackerPolicy
		var raw string
		var enabled bool
		if err := rows.Scan(&item.TrackerKey, &item.SiteID, &item.TrackerHost, &item.IntervalSeconds, &item.WaitMessageDigest, &item.WaitSeconds, &item.DeleteProtection, &raw, &enabled); err != nil {
			return nil, err
		}
		var hosts []string
		if err := json.Unmarshal([]byte(raw), &hosts); err != nil {
			return nil, err
		}
		if !enabled || !slices.Contains(hosts, item.TrackerHost) {
			return nil, ErrTrackerPolicyInvalid
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ObserveTrackerWait retains a known wait through restarts. Repeated identical
// responses do not slide its deadline; clearing and later repeating a message
// creates a new wait. A still-active older deadline is never shortened.
func (s *InstanceReannounceStore) ObserveTrackerWait(ctx context.Context, instanceID int, hash string, addedOn int64, key, digest string, at time.Time, delay time.Duration) (time.Time, error) {
	if addedOn <= 0 || at.IsZero() || delay < 0 || delay > 30*24*time.Hour {
		return time.Time{}, ErrTrackerPolicyInvalid
	}
	deadline := at
	if delay > 0 {
		deadline = at.Add(delay)
	}
	var next int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO reannounce_tracker_waits(instance_id,torrent_hash,added_on,tracker_key,message_digest,started_ns,observed_ns,not_before_ns) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,torrent_hash,added_on,tracker_key) DO UPDATE SET message_digest=excluded.message_digest,started_ns=CASE WHEN reannounce_tracker_waits.message_digest=excluded.message_digest THEN reannounce_tracker_waits.started_ns ELSE excluded.started_ns END,observed_ns=excluded.observed_ns,not_before_ns=CASE WHEN reannounce_tracker_waits.message_digest=excluded.message_digest AND reannounce_tracker_waits.started_ns+?>reannounce_tracker_waits.not_before_ns THEN reannounce_tracker_waits.started_ns+? WHEN reannounce_tracker_waits.message_digest<>excluded.message_digest AND excluded.not_before_ns>reannounce_tracker_waits.not_before_ns THEN excluded.not_before_ns ELSE reannounce_tracker_waits.not_before_ns END WHERE reannounce_tracker_waits.observed_ns<=excluded.observed_ns RETURNING not_before_ns`, instanceID, strings.ToLower(hash), addedOn, key, digest, at.UnixNano(), at.UnixNano(), deadline.UnixNano(), int64(delay), int64(delay)).Scan(&next)
	return time.Unix(0, next), err
}
