// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type ReclaimUploadCounter struct {
	Hash     string
	AddedOn  int64
	Uploaded int64
}
type ReclaimUploadWindow struct {
	Bytes   int64
	Covered bool
}
type reclaimUploadSample struct {
	AddedOn  int64
	At       int64
	Uploaded int64
	Baseline sql.NullInt64
}

// CaptureReclaimUploads retains only the requested trailing window plus one
// maximum sample gap. The oldest enclosing sample makes Bytes a conservative
// upper bound for that window; missing coverage is never treated as zero upload.
func (s *AutomationStore) CaptureReclaimUploads(ctx context.Context, instanceID int, at time.Time, window, gap time.Duration, counters []ReclaimUploadCounter) (map[string]ReclaimUploadWindow, error) {
	if at.IsZero() || window <= 0 || gap <= 0 {
		return nil, errors.New("invalid upload observation window")
	}
	current := map[string]ReclaimUploadCounter{}
	for _, item := range counters {
		if item.Hash == "" || item.AddedOn <= 0 || item.Uploaded < 0 {
			return nil, errors.New("invalid upload counter")
		}
		current[item.Hash] = item
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM reclaim_upload_samples WHERE instance_id=? AND observed_ns<?`, instanceID, at.Add(-window).Add(-gap).UnixNano()); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT torrent_hash,added_on,MAX(observed_ns),MAX(uploaded),MAX(CASE WHEN observed_ns<=? THEN uploaded ELSE NULL END) FROM reclaim_upload_samples WHERE instance_id=? GROUP BY torrent_hash,added_on`, at.Add(-window).UnixNano(), instanceID)
	if err != nil {
		return nil, err
	}
	history := map[string]reclaimUploadSample{}
	for rows.Next() {
		var hash string
		var item reclaimUploadSample
		if err := rows.Scan(&hash, &item.AddedOn, &item.At, &item.Uploaded, &item.Baseline); err != nil {
			rows.Close()
			return nil, err
		}
		if previous, exists := history[hash]; !exists || item.At > previous.At {
			history[hash] = item
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return nil, rowErr
	}
	for hash, last := range history {
		item, present := current[hash]
		if !present || item.AddedOn != last.AddedOn || item.Uploaded < last.Uploaded || (at.UnixNano() == last.At && item.Uploaded != last.Uploaded) || at.UnixNano() < last.At || at.UnixNano()-last.At > int64(gap) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM reclaim_upload_samples WHERE instance_id=? AND torrent_hash=?`, instanceID, hash); err != nil {
				return nil, err
			}
			delete(history, hash)
		}
	}
	result := map[string]ReclaimUploadWindow{}
	for hash, item := range current {
		last, exists := history[hash]
		if !exists || last.At < at.UnixNano() {
			if _, err := tx.ExecContext(ctx, `INSERT INTO reclaim_upload_samples(instance_id,torrent_hash,added_on,observed_ns,uploaded) VALUES(?,?,?,?,?)`, instanceID, hash, item.AddedOn, at.UnixNano(), item.Uploaded); err != nil {
				return nil, err
			}
		}
		// Last-sample gaps and counter regression remove the complete history above,
		// so an enclosing baseline proves uninterrupted measured coverage.
		coverage := ReclaimUploadWindow{}
		if exists && last.Baseline.Valid {
			coverage.Covered = true
			coverage.Bytes = item.Uploaded - last.Baseline.Int64
		}
		result[hash] = coverage
	}
	return result, tx.Commit()
}
