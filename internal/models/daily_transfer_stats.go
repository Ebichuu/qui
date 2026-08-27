// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
)

const dailyTransferRetentionDays = 62

// DailyTransferStats is qui's persisted per-instance transfer total for one
// local calendar day.
type DailyTransferStats struct {
	InstanceID int    `json:"instanceId"`
	Date       string `json:"date"`
	Downloaded int64  `json:"downloaded"`
	Uploaded   int64  `json:"uploaded"`
	UpdatedAt  string `json:"updatedAt"`
}

type dailyTransferSample struct {
	DailyTransferStats
	LastAlltimeDL int64
	LastAlltimeUL int64
}

// DailyTransferStatsStore converts qBittorrent's monotonic all-time counters
// into calendar-day deltas. The mutex serializes the read-modify-write cycle
// across the background sampler and dashboard requests.
type DailyTransferStatsStore struct {
	db dbinterface.Querier
	mu sync.Mutex
}

func NewDailyTransferStatsStore(db dbinterface.Querier) *DailyTransferStatsStore {
	return &DailyTransferStatsStore{db: db}
}

func (s *DailyTransferStatsStore) Record(ctx context.Context, instanceID int, now time.Time, alltimeDL, alltimeUL int64) (*DailyTransferStats, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("daily transfer stats store is not configured")
	}
	if instanceID <= 0 {
		return nil, errors.New("instance ID must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	day := now.Format("2006-01-02")
	sampledAt := now.Format(time.RFC3339Nano)
	alltimeDL = max(alltimeDL, 0)
	alltimeUL = max(alltimeUL, 0)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin daily transfer transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	latest, err := getLatestDailyTransferSample(ctx, tx, instanceID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO instance_daily_transfer_stats (
				instance_id, day, downloaded, uploaded,
				last_alltime_dl, last_alltime_ul, last_sample_at
			) VALUES (?, ?, 0, 0, ?, ?, ?)
		`, instanceID, day, alltimeDL, alltimeUL, sampledAt); err != nil {
			return nil, fmt.Errorf("insert first daily transfer sample: %w", err)
		}
	} else {
		downloadDelta := transferCounterDelta(alltimeDL, latest.LastAlltimeDL)
		uploadDelta := transferCounterDelta(alltimeUL, latest.LastAlltimeUL)
		if latest.Date == day {
			if _, err := tx.ExecContext(ctx, `
				UPDATE instance_daily_transfer_stats
				SET downloaded = downloaded + ?, uploaded = uploaded + ?,
				    last_alltime_dl = ?, last_alltime_ul = ?, last_sample_at = ?
				WHERE instance_id = ? AND day = ?
			`, downloadDelta, uploadDelta, alltimeDL, alltimeUL, sampledAt, instanceID, day); err != nil {
				return nil, fmt.Errorf("update daily transfer sample: %w", err)
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO instance_daily_transfer_stats (
					instance_id, day, downloaded, uploaded,
					last_alltime_dl, last_alltime_ul, last_sample_at
				) VALUES (?, ?, ?, ?, ?, ?, ?)
			`, instanceID, day, downloadDelta, uploadDelta, alltimeDL, alltimeUL, sampledAt); err != nil {
				return nil, fmt.Errorf("insert daily transfer rollover sample: %w", err)
			}
		}
	}

	cutoff := now.AddDate(0, 0, -dailyTransferRetentionDays).Format("2006-01-02")
	if _, err := tx.ExecContext(ctx, `DELETE FROM instance_daily_transfer_stats WHERE day < ?`, cutoff); err != nil {
		return nil, fmt.Errorf("prune daily transfer samples: %w", err)
	}

	stats, err := getDailyTransferStats(ctx, tx, instanceID, day)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit daily transfer transaction: %w", err)
	}
	return stats, nil
}

func (s *DailyTransferStatsStore) Get(ctx context.Context, instanceID int, day string) (*DailyTransferStats, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("daily transfer stats store is not configured")
	}
	return getDailyTransferStats(ctx, s.db, instanceID, day)
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getLatestDailyTransferSample(ctx context.Context, db rowQuerier, instanceID int) (*dailyTransferSample, error) {
	row := db.QueryRowContext(ctx, `
		SELECT instance_id, day, downloaded, uploaded,
		       last_alltime_dl, last_alltime_ul
		FROM instance_daily_transfer_stats
		WHERE instance_id = ?
		ORDER BY day DESC
		LIMIT 1
	`, instanceID)

	var sample dailyTransferSample
	if err := row.Scan(
		&sample.InstanceID,
		&sample.Date,
		&sample.Downloaded,
		&sample.Uploaded,
		&sample.LastAlltimeDL,
		&sample.LastAlltimeUL,
	); err != nil {
		return nil, err
	}
	return &sample, nil
}

func getDailyTransferStats(ctx context.Context, db rowQuerier, instanceID int, day string) (*DailyTransferStats, error) {
	row := db.QueryRowContext(ctx, `
		SELECT instance_id, day, downloaded, uploaded, CAST(last_sample_at AS TEXT)
		FROM instance_daily_transfer_stats
		WHERE instance_id = ? AND day = ?
	`, instanceID, day)

	var stats DailyTransferStats
	if err := row.Scan(&stats.InstanceID, &stats.Date, &stats.Downloaded, &stats.Uploaded, &stats.UpdatedAt); err != nil {
		return nil, err
	}
	return &stats, nil
}

func transferCounterDelta(current, previous int64) int64 {
	if current < previous {
		// qBittorrent was reset or reinstalled. Count the new counter value
		// instead of producing a negative daily total.
		return current
	}
	return current - previous
}
