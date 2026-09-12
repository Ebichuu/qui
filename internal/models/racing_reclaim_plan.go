// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
	"github.com/autobrr/qui/pkg/fileallocation"
)

type RacingReclaimSpent struct {
	Deletes           int   `json:"deletes"`
	CapacityBytes     int64 `json:"capacityBytes"`
	RecentUploadBytes int64 `json:"recentUploadBytes"`
	OvershootBytes    int64 `json:"overshootBytes"`
}

type RacingReclaimItem struct {
	Hash              string `json:"hash"`
	AddedOn           int64  `json:"addedOn"`
	CapacityBytes     int64  `json:"capacityBytes"`
	RecentUploadBytes int64  `json:"recentUploadBytes"`
}

type RacingReclaimPlan struct {
	CandidateKey          string              `json:"candidateKey"`
	CandidateUpdatedAt    string              `json:"candidateUpdatedAt"`
	InstanceID            int                 `json:"instanceId"`
	PoolID                int                 `json:"poolId"`
	ConfigurationRevision int64               `json:"configurationRevision"`
	Deadline              time.Time           `json:"deadline"`
	ObservedAt            time.Time           `json:"observedAt"`
	Frozen                RacingReclaimPolicy `json:"frozen"`
	Spent                 RacingReclaimSpent  `json:"spent"`
	DeficitBytes          int64               `json:"deficitBytes"`
	Items                 []RacingReclaimItem `json:"items"`
	State                 string              `json:"state"`
}

func reclaimPlan(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, key string) (*RacingReclaimPlan, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT plan_json FROM racing_reclaim_plans WHERE candidate_key=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plan RacingReclaimPlan
	if err = json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (s *RacingStore) ReclaimPlan(ctx context.Context, key string) (*RacingReclaimPlan, error) {
	return reclaimPlan(ctx, s.db, key)
}

func (s *RacingStore) currentReclaimPolicy(ctx context.Context, id int) (*RacingReclaimPolicy, int64, error) {
	config, err := s.ReclaimConfiguration(ctx)
	if err != nil {
		return nil, 0, err
	}
	for _, item := range config.Effective {
		if item.InstanceID == id && item.Policy != nil && item.Policy.Enabled {
			return item.Policy, config.Revision, nil
		}
	}
	return nil, config.Revision, ErrRacingStale
}

// SaveReclaimPlan freezes ceilings on first preparation. Replanning cannot move
// the event to a different target, erase charges, or overwrite an unresolved step.
func (s *RacingStore) SaveReclaimPlan(ctx context.Context, plan RacingReclaimPlan) error {
	current, revision, err := s.currentReclaimPolicy(ctx, plan.InstanceID)
	if err != nil {
		return err
	}
	if revision != plan.ConfigurationRevision {
		return ErrRacingStale
	}
	_, err = s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := s.lockExecution(ctx, tx, &revision); err != nil {
			return 0, err
		}
		old, err := reclaimPlan(ctx, tx, plan.CandidateKey)
		if err != nil {
			return 0, err
		}
		plan.Frozen = *current
		plan.Spent = RacingReclaimSpent{}
		if old != nil {
			if old.InstanceID != plan.InstanceID || old.PoolID != plan.PoolID || old.State != "awaiting_executor" {
				return 0, ErrRacingIntentState
			}
			plan.Frozen, plan.Spent = old.Frozen, old.Spent
			if old.Deadline.Before(plan.Deadline) {
				plan.Deadline = old.Deadline
			}
		}
		plan.State = "awaiting_executor"
		if err := validateReclaimPlan(plan, *current, time.Now()); err != nil {
			return 0, err
		}
		if err := s.checkReclaimCandidate(ctx, tx, plan); err != nil {
			return 0, err
		}
		raw, err := json.Marshal(plan)
		if err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO racing_reclaim_plans(candidate_key,instance_id,plan_json,updated_at) VALUES(?,?,?,?) ON CONFLICT(candidate_key) DO UPDATE SET plan_json=excluded.plan_json,updated_at=excluded.updated_at`, plan.CandidateKey, plan.InstanceID, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
		return 0, err
	})
	return err
}

func (s *RacingStore) checkReclaimCandidate(ctx context.Context, tx dbinterface.TxQuerier, plan RacingReclaimPlan) error {
	query := `SELECT c.updated_at,m.observed_at FROM racing_candidates c LEFT JOIN racing_candidate_metadata m ON m.candidate_key=c.candidate_key WHERE c.candidate_key=? AND c.state='ready' AND NOT EXISTS(SELECT 1 FROM racing_discoveries d WHERE d.site_id=c.site_id AND d.event_key=c.event_key AND d.revision>d.evaluated_revision AND (c.source_scope=0 OR d.source_id=c.source_scope))`
	if dbinterface.DialectOf(s.db) == "postgres" {
		query += " FOR SHARE OF c"
	}
	var updated string
	var metadata *string
	if err := tx.QueryRowContext(ctx, query, plan.CandidateKey).Scan(&updated, &metadata); err != nil {
		return err
	}
	if updated != plan.CandidateUpdatedAt {
		return ErrRacingStale
	}
	if metadata != nil {
		proof, err := time.Parse(time.RFC3339Nano, *metadata)
		if err != nil {
			return err
		}
		evaluated, err := time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return err
		}
		if proof.After(evaluated) {
			return ErrRacingStale
		}
	}
	return nil
}

func validateReclaimPlan(plan RacingReclaimPlan, current RacingReclaimPolicy, now time.Time) error {
	if plan.CandidateKey == "" || plan.InstanceID <= 0 || plan.PoolID <= 0 || plan.DeficitBytes <= 0 || !racingFresh(plan.ObservedAt, now) || !now.Before(plan.Deadline) || len(plan.Items) == 0 {
		return ErrRacingStale
	}
	if !current.Enabled || !plan.Frozen.Enabled || current.RecentUploadWindowSeconds != plan.Frozen.RecentUploadWindowSeconds {
		return ErrRacingStale
	}
	spent := plan.Spent
	if spent.Deletes < 0 || spent.CapacityBytes < 0 || spent.RecentUploadBytes < 0 || spent.OvershootBytes < 0 {
		return ErrRacingInvalid
	}
	if len(plan.Items) > min(plan.Frozen.MaxDeletes, current.MaxDeletes)-spent.Deletes {
		return ErrRacingCapacity
	}
	var capacity, upload int64
	seen := map[string]bool{}
	for _, item := range plan.Items {
		hash := strings.ToLower(item.Hash)
		if hash == "" || item.AddedOn <= 0 || seen[hash] || item.CapacityBytes <= 0 || item.RecentUploadBytes < 0 || item.CapacityBytes > math.MaxInt64-capacity || item.RecentUploadBytes > math.MaxInt64-upload {
			return ErrRacingInvalid
		}
		seen[hash] = true
		capacity += item.CapacityBytes
		upload += item.RecentUploadBytes
	}
	if capacity < plan.DeficitBytes || capacity > min(plan.Frozen.MaxReclaimBytes, current.MaxReclaimBytes)-spent.CapacityBytes || upload > min(plan.Frozen.MaxRecentUploadBytes, current.MaxRecentUploadBytes)-spent.RecentUploadBytes || capacity-plan.DeficitBytes > min(plan.Frozen.MaxOvershootBytes, current.MaxOvershootBytes)-spent.OvershootBytes {
		return ErrRacingCapacity
	}
	return nil
}

// BeginReclaimDelete is the store boundary for a future verified executor. It
// charges the first step and claims the shared deletion ledger atomically. It
// never sends a request or treats task absence as physical space confirmation.
func (s *RacingStore) BeginReclaimDelete(ctx context.Context, key, operation string, baseline fileallocation.ReleaseBaseline) error {
	snapshot, err := s.ReclaimPlan(ctx, key)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return ErrRacingStale
	}
	current, revision, err := s.currentReclaimPolicy(ctx, snapshot.InstanceID)
	if err != nil {
		return err
	}
	_, err = s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := s.lockExecution(ctx, tx, &revision); err != nil {
			return 0, err
		}
		plan, err := reclaimPlan(ctx, tx, key)
		if err != nil {
			return 0, err
		}
		if plan == nil || plan.State != "awaiting_executor" || revision != plan.ConfigurationRevision {
			return 0, ErrRacingStale
		}
		if err := validateReclaimPlan(*plan, *current, time.Now()); err != nil {
			return 0, err
		}
		if err := s.checkReclaimCandidate(ctx, tx, *plan); err != nil {
			return 0, err
		}
		var occupied bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM racing_reclaim_releases WHERE pool_id=? AND state='pending')`, plan.PoolID).Scan(&occupied); err != nil {
			return 0, err
		}
		if occupied {
			return 0, ErrRacingIntentState
		}
		item := plan.Items[0]
		if operation == "" || baseline.Root == "" || len(baseline.Files) == 0 || len(baseline.Files) > 10000 || baseline.ExpectedBytes != item.CapacityBytes || baseline.Space.Available < 0 || !racingFresh(baseline.Space.ObservedAt, time.Now()) {
			return 0, ErrRacingInvalid
		}
		// A baseline taken before the preceding pool receipt could count the
		// same net recovery twice, even when both observations are still fresh.
		var previousObservation string
		err = tx.QueryRowContext(ctx, `SELECT observation_json FROM racing_reclaim_releases WHERE pool_id=? AND state='observed' ORDER BY updated_at DESC LIMIT 1`, plan.PoolID).Scan(&previousObservation)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if err == nil {
			var previous fileallocation.Space
			if err := json.Unmarshal([]byte(previousObservation), &previous); err != nil {
				return 0, err
			}
			if !baseline.Space.ObservedAt.After(previous.ObservedAt) {
				return 0, ErrRacingStale
			}
		}
		if err := s.claimAutomaticDelete(ctx, tx, plan.InstanceID, operation, "official-reclaim", DeleteModeWithFiles, []DeleteIdentity{{Hash: strings.ToLower(item.Hash), AddedOn: item.AddedOn}}); err != nil {
			return 0, err
		}
		charge := RacingReclaimSpent{Deletes: 1, CapacityBytes: item.CapacityBytes, RecentUploadBytes: item.RecentUploadBytes, OvershootBytes: max(0, item.CapacityBytes-plan.DeficitBytes)}
		plan.Spent.Deletes++
		plan.Spent.CapacityBytes += charge.CapacityBytes
		plan.Spent.RecentUploadBytes += charge.RecentUploadBytes
		plan.Spent.OvershootBytes += charge.OvershootBytes
		plan.State = "awaiting_release"
		raw, _ := json.Marshal(plan)
		if _, err := tx.ExecContext(ctx, `UPDATE racing_reclaim_plans SET plan_json=?,updated_at=? WHERE candidate_key=?`, string(raw), time.Now().UTC().Format(time.RFC3339Nano), key); err != nil {
			return 0, err
		}
		raw, _ = json.Marshal(charge)
		if _, err = tx.ExecContext(ctx, `INSERT INTO racing_reclaim_charges(operation_id,candidate_key,charge_json) VALUES(?,?,?)`, operation, key, string(raw)); err != nil {
			return 0, err
		}
		raw, err = json.Marshal(baseline)
		if err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO racing_reclaim_releases(operation_id,pool_id,baseline_json,state,updated_at) VALUES(?,?,?,'pending',?)`, operation, plan.PoolID, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
		return 0, err
	})
	return err
}

// ReclaimPlans returns ledger history; it is not a list of authorized deletions.
func (s *RacingStore) ReclaimPlans(ctx context.Context) ([]RacingReclaimPlan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT plan_json FROM racing_reclaim_plans ORDER BY updated_at DESC,candidate_key LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RacingReclaimPlan{}
	for rows.Next() {
		var raw string
		var plan RacingReclaimPlan
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &plan); err != nil {
			return nil, err
		}
		result = append(result, plan)
	}
	return result, rows.Err()
}
