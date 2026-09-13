// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
)

const racingIntentColumns = `candidate_key,instance_id,plan_json,state,reason,reserved_at,submitted_at,accepted_at,confirmed_at,runnable_at,transferred_at,observed_at,updated_at`

func racingHashValid(hash string, length int) bool {
	if hash == "" {
		return true
	}
	decoded, err := hex.DecodeString(hash)
	return err == nil && len(decoded) == length && hash == strings.ToLower(hash)
}

func validateRacingPlan(plan RacingAddPlan, metainfo []byte) error {
	if plan.CandidateKey == "" || plan.SiteID <= 0 || plan.InstanceID <= 0 || plan.Policy.InstanceID != plan.InstanceID || !plan.Policy.Enabled || plan.Rule.ID <= 0 || plan.SizeBytes <= 0 || len(metainfo) == 0 || len(metainfo) > 8<<20 || len(plan.PoolIDs) == 0 || plan.CandidateUpdatedAt == "" {
		return racingInvalid("incomplete add plan")
	}
	if (plan.HashV1 == "" && plan.HashV2 == "") || !racingHashValid(plan.HashV1, 20) || !racingHashValid(plan.HashV2, 32) {
		return racingInvalid("verified torrent hash")
	}
	unique, err := racingIDs(plan.PoolIDs)
	if err != nil || len(unique) != len(plan.PoolIDs) {
		return racingInvalid("unique storage pools")
	}
	if _, err := time.Parse(time.RFC3339Nano, plan.FirstSeenAt); err != nil {
		return racingInvalid("first seen time")
	}
	if _, err := time.Parse(time.RFC3339Nano, plan.Deadline); err != nil {
		return racingInvalid("receive deadline")
	}
	return nil
}

// ReserveAdd makes one atomic promise against all of a candidate's disks. A
// second process uses the same database lock, so group membership cannot bypass
// the shared ledger. Metainfo is copied into the intent before any send.
func (s *RacingStore) ReserveAdd(ctx context.Context, input RacingReservation) error {
	if err := validateRacingPlan(input.Plan, input.Metainfo); err != nil {
		return err
	}
	plan := input.Plan
	encoded, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	encrypted := base64.RawStdEncoding.EncodeToString(s.secrets.Seal(nil, nil, input.Metainfo, []byte("add-intent:"+plan.CandidateKey)))
	_, err = s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := s.lockExecution(ctx, tx, &plan.ConfigurationRevision); err != nil {
			return 0, err
		}
		reclaim, err := reclaimPlan(ctx, tx, plan.CandidateKey)
		if err != nil {
			return 0, err
		}
		if reclaim != nil && reclaim.State == "awaiting_release" {
			return 0, ErrRacingIntentState
		}
		if err := s.checkAddPlan(ctx, tx, plan); err != nil {
			return 0, err
		}
		var state string
		err = tx.QueryRowContext(ctx, "SELECT state FROM racing_add_intents WHERE candidate_key=?", plan.CandidateKey).Scan(&state)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if state != "" && state != "cancelled" {
			return 0, ErrRacingIntentExists
		}
		var duplicates int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM racing_add_intents WHERE candidate_key<>? AND state NOT IN ('cancelled','retired') AND ((hash_v1<>'' AND hash_v1=?) OR (hash_v2<>'' AND hash_v2=?))`, plan.CandidateKey, plan.HashV1, plan.HashV2).Scan(&duplicates); err != nil {
			return 0, err
		}
		if duplicates > 0 {
			return 0, ErrRacingIntentExists
		}
		if err := s.checkAddCapacity(ctx, tx, input); err != nil {
			return 0, err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = tx.ExecContext(ctx, `INSERT INTO racing_add_intents(candidate_key,instance_id,hash_v1,hash_v2,plan_json,metainfo_ciphertext,state,reserved_at,updated_at) VALUES(?,?,?,?,?,?,'reserved',?,?) ON CONFLICT(candidate_key) DO UPDATE SET instance_id=excluded.instance_id,hash_v1=excluded.hash_v1,hash_v2=excluded.hash_v2,plan_json=excluded.plan_json,metainfo_ciphertext=excluded.metainfo_ciphertext,state='reserved',reason='',reserved_at=excluded.reserved_at,updated_at=excluded.updated_at`, plan.CandidateKey, plan.InstanceID, plan.HashV1, plan.HashV2, string(encoded), encrypted, now, now)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM racing_space_commitments WHERE candidate_key=?", plan.CandidateKey); err != nil {
			return 0, err
		}
		for _, pool := range plan.PoolIDs {
			if _, err := tx.ExecContext(ctx, "INSERT INTO racing_space_commitments(candidate_key,storage_pool_id,bytes) VALUES(?,?,?)", plan.CandidateKey, pool, plan.SizeBytes); err != nil {
				return 0, err
			}
		}
		return 0, nil
	})
	return err
}

func (s *RacingStore) checkAddPlan(ctx context.Context, tx dbinterface.TxQuerier, plan RacingAddPlan) error {
	deadline, err := time.Parse(time.RFC3339Nano, plan.Deadline)
	if err != nil || !time.Now().Before(deadline) {
		return ErrRacingStale
	}
	var active, enabled bool
	var updated string
	query := `SELECT i.is_active,p.enabled,p.updated_at FROM instances i JOIN racing_instance_policies p ON p.instance_id=i.id WHERE i.id=?`
	if dbinterface.DialectOf(s.db) == "postgres" {
		query += " FOR SHARE OF i,p"
	}
	if err := tx.QueryRowContext(ctx, query, plan.InstanceID).Scan(&active, &enabled, &updated); err != nil {
		return err
	}
	if !active || !enabled || updated != plan.Policy.UpdatedAt {
		return ErrRacingStale
	}
	query = `SELECT updated_at FROM racing_candidates WHERE candidate_key=? AND site_id=? AND state='ready' AND NOT EXISTS (SELECT 1 FROM racing_discoveries d WHERE d.site_id=racing_candidates.site_id AND d.event_key=racing_candidates.event_key AND d.revision>d.evaluated_revision AND (racing_candidates.source_scope=0 OR d.source_id=racing_candidates.source_scope))`
	if dbinterface.DialectOf(s.db) == "postgres" {
		query += " FOR SHARE"
	}
	if err := tx.QueryRowContext(ctx, query, plan.CandidateKey, plan.SiteID).Scan(&updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRacingStale
		}
		return err
	}
	if updated != plan.CandidateUpdatedAt {
		return ErrRacingStale
	}
	var metadataAt string
	err = tx.QueryRowContext(ctx, "SELECT observed_at FROM racing_candidate_metadata WHERE candidate_key=?", plan.CandidateKey).Scan(&metadataAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if metadataAt != "" {
		evidence, err := time.Parse(time.RFC3339Nano, metadataAt)
		if err != nil {
			return err
		}
		evaluated, err := time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return err
		}
		if evidence.After(evaluated) {
			return ErrRacingStale
		}
	}
	return nil
}

func racingFresh(at, now time.Time) bool {
	return !at.IsZero() && !at.After(now) && now.Sub(at) <= 5*time.Second
}

func (s *RacingStore) checkAddCapacity(ctx context.Context, tx dbinterface.TxQuerier, input RacingReservation) error {
	now := time.Now()
	if !racingFresh(input.ObservedAt, now) || input.ActiveDownloads < 0 {
		return ErrRacingStale
	}
	policy := input.Plan.Policy
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM racing_add_intents WHERE instance_id=? AND candidate_key<>? AND state IN ('reserved','submitted','accepted','unknown')`, input.Plan.InstanceID, input.Plan.CandidateKey).Scan(&pending); err != nil {
		return err
	}
	if pending >= policy.MaxConcurrentAdds || input.ActiveDownloads >= policy.MaxActiveDownloads || pending >= policy.MaxActiveDownloads-input.ActiveDownloads {
		return ErrRacingCapacity
	}
	for _, pool := range input.Plan.PoolIDs {
		index := slices.IndexFunc(input.Pools, func(b RacingPoolBudget) bool { return b.StoragePoolID == pool })
		if index < 0 {
			return ErrRacingStale
		}
		budget := input.Pools[index]
		if !racingFresh(budget.ObservedAt, now) {
			return ErrRacingStale
		}
		if budget.AvailableBytes < 0 || policy.MinFreeBytes > budget.AvailableBytes || input.Plan.SizeBytes > budget.AvailableBytes-policy.MinFreeBytes {
			return ErrRacingCapacity
		}
		remaining := budget.AvailableBytes - policy.MinFreeBytes - input.Plan.SizeBytes
		rows, err := tx.QueryContext(ctx, `SELECT c.candidate_key,c.bytes,i.state,i.confirmed_at FROM racing_space_commitments c JOIN racing_add_intents i ON i.candidate_key=c.candidate_key WHERE c.storage_pool_id=? AND c.candidate_key<>?`, pool, input.Plan.CandidateKey)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key, state string
			var size int64
			var confirmed *string
			if err := rows.Scan(&key, &size, &state, &confirmed); err != nil {
				rows.Close()
				return err
			}
			// Only confirmed identity can replace a reservation with observed remaining
			// writes. The caller must derive coverage and capacity from one snapshot.
			if state == "confirmed" && confirmed != nil && slices.Contains(budget.CoveredCandidates, key) {
				at, err := time.Parse(time.RFC3339Nano, *confirmed)
				if err == nil && !at.After(budget.ObservedAt) {
					continue
				}
			}
			if size > remaining {
				rows.Close()
				return ErrRacingCapacity
			}
			remaining -= size
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// SubmitAdd is the durable send boundary. Exactly one caller can cross it.
// A crash after this commit must reconcile the original target, never resend.
func (s *RacingStore) SubmitAdd(ctx context.Context, expected RacingAddIntent, budgets []RacingPoolBudget, active int, observed time.Time) error {
	key := expected.CandidateKey
	_, err := s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := s.lockExecution(ctx, tx, nil); err != nil {
			return 0, err
		}
		intent, err := readRacingIntent(tx.QueryRowContext(ctx, "SELECT "+racingIntentColumns+" FROM racing_add_intents WHERE candidate_key=?", key))
		if err != nil {
			return 0, err
		}
		if intent.State != "reserved" || intent.ReservedAt != expected.ReservedAt {
			return 0, ErrRacingIntentState
		}
		if err := s.lockExecution(ctx, tx, &intent.Plan.ConfigurationRevision); err != nil {
			return 0, err
		}
		if err := s.checkAddPlan(ctx, tx, intent.Plan); err != nil {
			return 0, err
		}
		if err := s.checkAddCapacity(ctx, tx, RacingReservation{Plan: intent.Plan, Pools: budgets, ActiveDownloads: active, ObservedAt: observed}); err != nil {
			return 0, err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = tx.ExecContext(ctx, "UPDATE racing_add_intents SET state='submitted',submitted_at=?,updated_at=? WHERE candidate_key=?", now, now, key)
		return 0, err
	})
	return err
}

// CancelReserved only releases work that provably has not crossed SubmitAdd.
func (s *RacingStore) CancelReserved(ctx context.Context, expected RacingAddIntent) error {
	key := expected.CandidateKey
	_, err := s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := s.lockExecution(ctx, tx, nil); err != nil {
			return 0, err
		}
		result, err := tx.ExecContext(ctx, "UPDATE racing_add_intents SET state='cancelled',reason='reevaluation_required',updated_at=? WHERE candidate_key=? AND state='reserved' AND submitted_at IS NULL AND reserved_at=?", time.Now().UTC().Format(time.RFC3339Nano), key, expected.ReservedAt)
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if count != 1 {
			return 0, ErrRacingIntentState
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM racing_space_commitments WHERE candidate_key=?", key)
		return 0, err
	})
	return err
}

// RecordAddResult cannot release a possibly accepted request. Any transport or
// response failure remains unknown until the task and its trackers are checked.
func (s *RacingStore) RecordAddResult(ctx context.Context, key string, accepted bool) error {
	_, err := s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := s.lockExecution(ctx, tx, nil); err != nil {
			return 0, err
		}
		state := "unknown"
		var acceptedAt *string
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if accepted {
			state = "accepted"
			acceptedAt = &now
		}
		result, err := tx.ExecContext(ctx, `UPDATE racing_add_intents SET state=?,accepted_at=COALESCE(accepted_at,?),updated_at=? WHERE candidate_key=? AND state IN ('submitted','unknown')`, state, acceptedAt, now, key)
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if count == 0 {
			var existing string
			if err := tx.QueryRowContext(ctx, "SELECT state FROM racing_add_intents WHERE candidate_key=?", key).Scan(&existing); err != nil {
				return 0, err
			}
			if existing != "confirmed" && existing != "accepted" {
				return 0, ErrRacingIntentState
			}
		}
		return 0, nil
	})
	return err
}

// ConfirmAdd is called only after matching real hash and site/account tracker
// identity. Later observations advance timestamps without downgrading success.
func (s *RacingStore) ConfirmAdd(ctx context.Context, key string, observed time.Time, runnable, transferred bool) error {
	if !racingFresh(observed, time.Now()) {
		return ErrRacingStale
	}
	_, err := s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := s.lockExecution(ctx, tx, nil); err != nil {
			return 0, err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		at := observed.UTC().Format(time.RFC3339Nano)
		var runAt, transferAt *string
		if runnable {
			runAt = &at
		}
		if transferred {
			transferAt = &at
		}
		result, err := tx.ExecContext(ctx, `UPDATE racing_add_intents SET state='confirmed',reason='',missing_since=NULL,missing_observed_at=NULL,confirmed_at=COALESCE(confirmed_at,?),runnable_at=COALESCE(runnable_at,?),transferred_at=COALESCE(transferred_at,?),observed_at=?,updated_at=? WHERE candidate_key=? AND state IN ('submitted','accepted','unknown','confirmed')`, at, runAt, transferAt, at, now, key)
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if count != 1 {
			return 0, ErrRacingIntentState
		}
		return 0, nil
	})
	return err
}

type racingIntentScanner interface{ Scan(...any) error }

func readRacingIntent(row racingIntentScanner) (RacingAddIntent, error) {
	var item RacingAddIntent
	var plan string
	err := row.Scan(&item.CandidateKey, &item.InstanceID, &plan, &item.State, &item.Reason, &item.ReservedAt, &item.SubmittedAt, &item.AcceptedAt, &item.ConfirmedAt, &item.RunnableAt, &item.TransferredAt, &item.ObservedAt, &item.UpdatedAt)
	if err == nil {
		err = json.Unmarshal([]byte(plan), &item.Plan)
	}
	return item, err
}

func (s *RacingStore) AddIntents(ctx context.Context, after string, limit int) ([]RacingAddIntent, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+racingIntentColumns+" FROM racing_add_intents WHERE candidate_key>? ORDER BY candidate_key LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RacingAddIntent{}
	for rows.Next() {
		item, err := readRacingIntent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *RacingStore) AddIntent(ctx context.Context, key string) (RacingAddIntent, error) {
	return readRacingIntent(s.db.QueryRowContext(ctx, "SELECT "+racingIntentColumns+" FROM racing_add_intents WHERE candidate_key=?", key))
}

func (s *RacingStore) IntentMetainfo(ctx context.Context, key string) ([]byte, error) {
	var ciphertext string
	if err := s.db.QueryRowContext(ctx, "SELECT metainfo_ciphertext FROM racing_add_intents WHERE candidate_key=?", key).Scan(&ciphertext); err != nil {
		return nil, err
	}
	return s.openRacingMetainfo(ciphertext, "add-intent:"+key)
}

func (s *RacingStore) CandidateMetainfo(ctx context.Context, key string) ([]byte, error) {
	var ciphertext string
	if err := s.db.QueryRowContext(ctx, "SELECT m.private_ciphertext FROM racing_candidate_metadata m JOIN racing_sources s ON s.id=m.source_id JOIN racing_sites site ON site.id=s.site_id WHERE m.candidate_key=? AND m.source_revision=s.updated_at AND m.site_revision=site.updated_at AND s.enabled=1 AND site.enabled=1", key).Scan(&ciphertext); err != nil {
		return nil, err
	}
	return s.openRacingMetainfo(ciphertext, "candidate-metainfo:"+key)
}
func (s *RacingStore) openRacingMetainfo(ciphertext, purpose string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}
	return s.secrets.Open(nil, nil, decoded, []byte(purpose))
}

func (s *RacingStore) RecordIntentReason(ctx context.Context, key, reason string) error {
	switch reason {
	case "", "target_unavailable", "tracker_identity_unverified", "existing_tracker_mismatch", "metainfo_unavailable", "awaiting_task":
	default:
		return racingInvalid("intent observation reason")
	}
	_, err := s.db.ExecContext(ctx, "UPDATE racing_add_intents SET reason=?,updated_at=? WHERE candidate_key=? AND reason<>?", reason, time.Now().UTC().Format(time.RFC3339Nano), key, reason)
	return err
}

// ObserveConfirmedMissing retires only previously confirmed tasks after repeated
// fresh absence for 30 seconds. Unknown submissions never enter this path. The
// historical event remains immutable, but a later revival can make a new plan.
func (s *RacingStore) ObserveConfirmedMissing(ctx context.Context, expected RacingAddIntent, observed time.Time) error {
	if !racingFresh(observed, time.Now()) {
		return ErrRacingStale
	}
	_, err := s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := s.lockExecution(ctx, tx, nil); err != nil {
			return 0, err
		}
		var state, reserved string
		var since, last, present *string
		err := tx.QueryRowContext(ctx, "SELECT state,reserved_at,missing_since,missing_observed_at,observed_at FROM racing_add_intents WHERE candidate_key=?", expected.CandidateKey).Scan(&state, &reserved, &since, &last, &present)
		if err != nil {
			return 0, err
		}
		if state != "confirmed" || reserved != expected.ReservedAt {
			return 0, ErrRacingIntentState
		}
		if present != nil {
			lastPresent, err := time.Parse(time.RFC3339Nano, *present)
			if err != nil {
				return 0, err
			}
			if !observed.After(lastPresent) {
				return 0, nil
			}
		}
		at := observed.UTC().Format(time.RFC3339Nano)
		previous, start := time.Time{}, time.Time{}
		if last != nil {
			previous, _ = time.Parse(time.RFC3339Nano, *last)
		}
		if since != nil {
			start, _ = time.Parse(time.RFC3339Nano, *since)
		}
		if !previous.IsZero() && !observed.After(previous) {
			return 0, nil
		}
		if start.IsZero() || previous.IsZero() || observed.Sub(previous) > 10*time.Second {
			start = observed
		}
		if observed.Sub(start) >= 30*time.Second {
			if _, err := tx.ExecContext(ctx, "DELETE FROM racing_space_commitments WHERE candidate_key=?", expected.CandidateKey); err != nil {
				return 0, err
			}
			_, err = tx.ExecContext(ctx, "UPDATE racing_add_intents SET state='retired',reason='task_removed',missing_observed_at=?,updated_at=? WHERE candidate_key=?", at, time.Now().UTC().Format(time.RFC3339Nano), expected.CandidateKey)
		} else {
			_, err = tx.ExecContext(ctx, "UPDATE racing_add_intents SET reason='awaiting_task',missing_since=?,missing_observed_at=?,updated_at=? WHERE candidate_key=?", start.UTC().Format(time.RFC3339Nano), at, time.Now().UTC().Format(time.RFC3339Nano), expected.CandidateKey)
		}
		return 0, err
	})
	return err
}
