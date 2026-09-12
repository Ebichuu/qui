// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"encoding/json"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
	"github.com/autobrr/qui/pkg/fileallocation"
)

// ReclaimRelease is internal evidence, not an authorization or a public API DTO.
// File paths must never be included in the public plan history.
type ReclaimRelease struct {
	OperationID  string
	CandidateKey string
	InstanceID   int
	PoolID       int
	Baseline     fileallocation.ReleaseBaseline
	State        string
}

func (s *RacingStore) ReclaimRelease(ctx context.Context, operation string) (*ReclaimRelease, error) {
	var item ReclaimRelease
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT r.operation_id,c.candidate_key,p.instance_id,r.pool_id,r.baseline_json,r.state FROM racing_reclaim_releases r JOIN racing_reclaim_charges c ON c.operation_id=r.operation_id JOIN racing_reclaim_plans p ON p.candidate_key=c.candidate_key WHERE r.operation_id=?`, operation).Scan(&item.OperationID, &item.CandidateKey, &item.InstanceID, &item.PoolID, &raw, &item.State)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &item.Baseline); err != nil {
		return nil, err
	}
	return &item, nil
}

// RecordReclaimRelease accepts a fresh observation only after the caller has
// checked all baseline paths with fileallocation.ObserveRelease. The transaction
// additionally requires accepted-and-confirmed task absence. It releases the
// pool lease, never refunds spent budgets, and discards all old candidate evidence.
func (s *RacingStore) RecordReclaimRelease(ctx context.Context, operation string, observed fileallocation.Space) error {
	_, err := s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := s.lockExecution(ctx, tx, nil); err != nil {
			return 0, err
		}
		var key, baselineJSON, releaseState, deleteState, submitted, confirmed string
		var pool int
		err := tx.QueryRowContext(ctx, `SELECT c.candidate_key,r.pool_id,r.baseline_json,r.state,a.state,a.submitted_at,a.updated_at FROM racing_reclaim_releases r JOIN racing_reclaim_charges c ON c.operation_id=r.operation_id JOIN automatic_delete_intents a ON a.operation_id=r.operation_id AND a.owner='official-reclaim' WHERE r.operation_id=?`, operation).Scan(&key, &pool, &baselineJSON, &releaseState, &deleteState, &submitted, &confirmed)
		if err != nil {
			return 0, err
		}
		if releaseState != "pending" || deleteState != "confirmed" {
			return 0, ErrRacingIntentState
		}
		var baseline fileallocation.ReleaseBaseline
		if err := json.Unmarshal([]byte(baselineJSON), &baseline); err != nil {
			return 0, err
		}
		sentAt, err := time.Parse(time.RFC3339Nano, submitted)
		if err != nil {
			return 0, err
		}
		confirmedAt, err := time.Parse(time.RFC3339Nano, confirmed)
		if err != nil {
			return 0, err
		}
		if observed.ObservedAt.Before(confirmedAt) {
			return 0, ErrRacingStale
		}
		if baseline.ExpectedBytes <= 0 || baseline.Space.ObservedAt.IsZero() || baseline.Space.Available < 0 || observed.Device != baseline.Space.Device || observed.RootID != baseline.Space.RootID || !racingFresh(observed.ObservedAt, time.Now()) || !observed.ObservedAt.After(sentAt) || !observed.ObservedAt.After(baseline.Space.ObservedAt) || observed.Available < baseline.Space.Available || observed.Available-baseline.Space.Available < baseline.ExpectedBytes {
			return 0, ErrRacingStale
		}
		plan, err := reclaimPlan(ctx, tx, key)
		if err != nil {
			return 0, err
		}
		if plan == nil || plan.State != "awaiting_release" || plan.PoolID != pool {
			return 0, ErrRacingIntentState
		}
		// Even after expiry or configuration disablement, record evidence for
		// the existing operation. A new step still needs a fresh valid plan.
		plan.State = "awaiting_executor"
		plan.Items = []RacingReclaimItem{}
		plan.ObservedAt = time.Time{}
		raw, err := json.Marshal(plan)
		if err != nil {
			return 0, err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `UPDATE racing_reclaim_plans SET plan_json=?,updated_at=? WHERE candidate_key=?`, string(raw), now, key); err != nil {
			return 0, err
		}
		raw, err = json.Marshal(observed)
		if err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE racing_reclaim_releases SET observation_json=?,state='observed',updated_at=? WHERE operation_id=? AND state='pending'`, string(raw), now, operation)
		return 0, err
	})
	return err
}
