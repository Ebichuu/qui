// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// TrackerObservation is qB's reported state, not an announce receipt or a
// statement about site accounting. Identity and messages are digests only.
type TrackerObservation struct {
	Key           string `json:"key"`
	Host          string `json:"host"`
	State         string `json:"state"`
	MessageDigest string `json:"messageDigest,omitempty"`
}

type ReannounceObservation struct {
	InstanceID      int                  `json:"instanceId"`
	Hash            string               `json:"hash"`
	AddedOn         int64                `json:"addedOn"`
	ObservedAt      time.Time            `json:"observedAt"`
	LocalObservedAt time.Time            `json:"localObservedAt"`
	LocalUploaded   int64                `json:"localUploaded"`
	Trackers        []TrackerObservation `json:"trackers"`
}

func (s *InstanceReannounceStore) SaveObservation(ctx context.Context, item ReannounceObservation) error {
	if item.InstanceID <= 0 || item.Hash == "" || item.AddedOn <= 0 || item.LocalUploaded < 0 || item.ObservedAt.IsZero() || item.LocalObservedAt.IsZero() || len(item.Trackers) > 1024 {
		return errors.New("invalid tracker observation")
	}
	encoded, err := json.Marshal(item.Trackers)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM reannounce_observations WHERE instance_id=? AND observed_ns<?`, item.InstanceID, item.ObservedAt.Add(-30*24*time.Hour).UnixNano()); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO reannounce_observations(instance_id,torrent_hash,added_on,observed_ns,local_observed_ns,local_uploaded,trackers_json) VALUES(?,?,?,?,?,?,?) ON CONFLICT(instance_id,torrent_hash) DO UPDATE SET added_on=excluded.added_on,observed_ns=excluded.observed_ns,local_observed_ns=excluded.local_observed_ns,local_uploaded=excluded.local_uploaded,trackers_json=excluded.trackers_json WHERE reannounce_observations.observed_ns<excluded.observed_ns`, item.InstanceID, strings.ToLower(item.Hash), item.AddedOn, item.ObservedAt.UnixNano(), item.LocalObservedAt.UnixNano(), item.LocalUploaded, string(encoded))
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return errors.New("stale tracker observation")
	}
	return tx.Commit()
}

// Observations returns bounded historical snapshots; callers must use their
// timestamps and generation rather than treating returned rows as live state.
func (s *InstanceReannounceStore) Observations(ctx context.Context, instanceID int) ([]ReannounceObservation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT torrent_hash,added_on,observed_ns,local_observed_ns,local_uploaded,trackers_json FROM reannounce_observations WHERE instance_id=? ORDER BY observed_ns DESC,torrent_hash LIMIT 100`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ReannounceObservation{}
	for rows.Next() {
		item := ReannounceObservation{InstanceID: instanceID}
		var observed, local int64
		var raw string
		if err := rows.Scan(&item.Hash, &item.AddedOn, &observed, &local, &item.LocalUploaded, &raw); err != nil {
			return nil, err
		}
		item.ObservedAt, item.LocalObservedAt = time.Unix(0, observed).UTC(), time.Unix(0, local).UTC()
		if err := json.Unmarshal([]byte(raw), &item.Trackers); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
