// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AutomationConditionObservation records measured time, not elapsed downtime.
type AutomationConditionObservation struct {
	RuleID      int
	RuleVersion string
	Hash        string
	AddedOn     int64
	DryRun      bool
	Elapsed     time.Duration
	Duration    time.Duration
	ObservedAt  time.Time
	Uploaded    int64
	Downloaded  int64
}

func (s *AutomationStore) ConditionObservations(ctx context.Context, instanceID int) ([]AutomationConditionObservation, error) {
	return s.conditionObservations(ctx, instanceID, "automation_condition_observations")
}
func (s *AutomationStore) ReclaimObservations(ctx context.Context, instanceID int) ([]AutomationConditionObservation, error) {
	return s.conditionObservations(ctx, instanceID, "reclaim_condition_observations")
}
func (s *AutomationStore) conditionObservations(ctx context.Context, instanceID int, table string) ([]AutomationConditionObservation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rule_id,rule_version,torrent_hash,added_on,dry_run,elapsed_ns,duration_ns,observed_at,uploaded,downloaded FROM `+table+` WHERE instance_id=?`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AutomationConditionObservation{}
	for rows.Next() {
		var item AutomationConditionObservation
		var dry int
		var at string
		if err := rows.Scan(&item.RuleID, &item.RuleVersion, &item.Hash, &item.AddedOn, &dry, &item.Elapsed, &item.Duration, &at, &item.Uploaded, &item.Downloaded); err != nil {
			return nil, err
		}
		item.DryRun = dry != 0
		item.ObservedAt, err = time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *AutomationStore) SaveConditionObservations(ctx context.Context, instanceID int, items []AutomationConditionObservation) error {
	return s.saveConditionObservations(ctx, instanceID, items, "automation_condition_observations")
}
func (s *AutomationStore) SaveReclaimObservations(ctx context.Context, instanceID int, items []AutomationConditionObservation) error {
	return s.saveConditionObservations(ctx, instanceID, items, "reclaim_condition_observations")
}

// Table names are fixed by the two internal callers, never supplied by API data.
func (s *AutomationStore) saveConditionObservations(ctx context.Context, instanceID int, items []AutomationConditionObservation, table string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE instance_id=?`, instanceID); err != nil {
		return err
	}
	for _, item := range items {
		if item.Elapsed < 0 || item.Duration <= 0 || item.ObservedAt.IsZero() || len(item.RuleVersion) != 64 || item.Hash == "" {
			return errors.New("invalid automation observation")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO `+table+`(instance_id,rule_id,rule_version,torrent_hash,added_on,dry_run,elapsed_ns,duration_ns,observed_at,uploaded,downloaded) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, instanceID, item.RuleID, item.RuleVersion, item.Hash, item.AddedOn, boolToInt(item.DryRun), item.Elapsed, item.Duration, item.ObservedAt.UTC().Format(time.RFC3339Nano), item.Uploaded, item.Downloaded)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *AutomationStore) DeleteCooldown(ctx context.Context, instanceID int) (time.Time, error) {
	var at string
	err := s.db.QueryRowContext(ctx, `SELECT attempted_at FROM automation_delete_cooldowns WHERE instance_id=?`, instanceID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, at)
}

// RecordDeleteCooldown precedes the network request: an unknown result must not
// erase the existing FREE_SPACE cooldown on restart.
func (s *AutomationStore) RecordDeleteCooldown(ctx context.Context, instanceID int, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO automation_delete_cooldowns(instance_id,attempted_at) VALUES (?,?) ON CONFLICT(instance_id) DO UPDATE SET attempted_at=excluded.attempted_at`, instanceID, at.UTC().Format(time.RFC3339Nano))
	return err
}
