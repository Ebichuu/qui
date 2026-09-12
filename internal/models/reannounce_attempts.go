// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *InstanceReannounceStore) ReannounceTiming(ctx context.Context, instanceID int, hash string) (int64, int64, error) {
	var started, deadline int64
	err := s.db.QueryRowContext(ctx, `SELECT started_ns,not_before_ns FROM reannounce_attempts WHERE instance_id=? AND torrent_hash=?`, instanceID, strings.ToLower(hash)).Scan(&started, &deadline)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return started, deadline, err
}

// BeginReannounce persists the next permitted send before network I/O. Both
// the original and current intervals apply, even after restart or a settings edit.
func (s *InstanceReannounceStore) BeginReannounce(ctx context.Context, instanceID int, hash string, now time.Time, interval time.Duration) (bool, error) {
	if instanceID <= 0 || hash == "" || now.IsZero() || interval <= 0 {
		return false, errors.New("invalid reannounce interval")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO reannounce_attempts(instance_id,torrent_hash,started_ns,not_before_ns) VALUES(?,?,?,?) ON CONFLICT(instance_id,torrent_hash) DO UPDATE SET started_ns=excluded.started_ns,not_before_ns=excluded.not_before_ns WHERE reannounce_attempts.not_before_ns<=excluded.started_ns AND reannounce_attempts.started_ns<=?`, instanceID, strings.ToLower(hash), now.UnixNano(), now.Add(interval).UnixNano(), now.Add(-interval).UnixNano())
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
