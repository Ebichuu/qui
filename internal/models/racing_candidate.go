// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
)

type RacingCandidateRecord struct {
	Key              string          `json:"key"`
	SiteID           int             `json:"siteId"`
	EventKey         string          `json:"eventKey"`
	SourceScope      int             `json:"sourceScope"`
	FirstSeenAt      string          `json:"firstSeenAt"`
	Candidate        json.RawMessage `json:"candidate"`
	Selection        json.RawMessage `json:"selection"`
	State            string          `json:"state"`
	NextEvaluationAt *string         `json:"nextEvaluationAt,omitempty"`
	UpdatedAt        string          `json:"updatedAt"`
}

// Pending revisions are durable wakeups. A full in-memory notification channel
// or a restart cannot lose an observation that has not yet been evaluated.
type RacingPendingBatch struct {
	Items     []RacingDiscovery
	ThroughID int64
}

func (s *RacingStore) PendingDiscoveries(ctx context.Context, afterID, throughID int64, limit int) (RacingPendingBatch, error) {
	result := RacingPendingBatch{Items: []RacingDiscovery{}, ThroughID: throughID}
	if limit < 1 || limit > 100 {
		limit = 100
	}
	if throughID == 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM racing_discoveries`).Scan(&result.ThroughID); err != nil {
			return result, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,source_id,site_id,event_key,public_item,eligible,first_seen_at,last_seen_at,revision FROM racing_discoveries WHERE revision>evaluated_revision AND id>? AND id<=? ORDER BY id LIMIT ?`, afterID, result.ThroughID, limit)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	result.Items, err = readDiscoveries(rows)
	return result, err
}

type RacingCandidateInput struct {
	Observations      []RacingDiscovery
	FirstSeenAt       string
	PreviousSelection json.RawMessage
}

func (s *RacingStore) EventDiscoveries(ctx context.Context, key string, siteID int, event string, sourceScope int) (RacingCandidateInput, error) {
	result := RacingCandidateInput{}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT first_seen_at,selection_json FROM racing_candidates WHERE candidate_key=?`, key).Scan(&result.FirstSeenAt, &previous)
	result.PreviousSelection = json.RawMessage(previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	query := `SELECT id,source_id,site_id,event_key,public_item,eligible,first_seen_at,last_seen_at,revision FROM racing_discoveries WHERE site_id=? AND event_key=?`
	args := []any{siteID, event}
	if sourceScope > 0 {
		query += " AND source_id=?"
		args = append(args, sourceScope)
	}
	query += " ORDER BY first_seen_at,id"
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return result, err
	}
	result.Observations, err = readDiscoveries(rows)
	rows.Close()
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (s *RacingStore) InvalidateCandidate(ctx context.Context, key string, selection []byte) error {
	_, err := s.db.ExecContext(ctx, `UPDATE racing_candidates SET state='rejected',selection_json=?,next_evaluation_at=NULL,updated_at=? WHERE candidate_key=?`, string(selection), time.Now().UTC().Format(time.RFC3339Nano), key)
	return err
}

func readDiscoveries(rows *sql.Rows) ([]RacingDiscovery, error) {
	result := []RacingDiscovery{}
	for rows.Next() {
		var item RacingDiscovery
		var public string
		if err := rows.Scan(&item.ID, &item.SourceID, &item.SiteID, &item.EventKey, &public, &item.Eligible, &item.FirstSeenAt, &item.LastSeenAt, &item.Revision); err != nil {
			return nil, err
		}
		item.Item = json.RawMessage(public)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *RacingStore) SaveCandidate(ctx context.Context, record RacingCandidateRecord, observed []RacingDiscovery) error {
	if record.Key == "" || !json.Valid(record.Candidate) || !json.Valid(record.Selection) {
		return errors.New("invalid racing candidate")
	}
	_, err := s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		// Re-observing unchanged evidence must not invalidate an in-flight
		// reservation. A newly fetched proof still needs an evaluation fence.
		updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
		var previous string
		var metadataAt *string
		err := tx.QueryRowContext(ctx, `SELECT c.updated_at,m.observed_at FROM racing_candidates c LEFT JOIN racing_candidate_metadata m ON m.candidate_key=c.candidate_key WHERE c.candidate_key=? AND c.candidate_json=? AND c.selection_json=? AND c.state=?`, record.Key, string(record.Candidate), string(record.Selection), record.State).Scan(&previous, &metadataAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if err == nil {
			newProof := false
			if metadataAt != nil {
				proofTime, err := time.Parse(time.RFC3339Nano, *metadataAt)
				if err != nil {
					return 0, err
				}
				previousTime, err := time.Parse(time.RFC3339Nano, previous)
				if err != nil {
					return 0, err
				}
				newProof = proofTime.After(previousTime)
			}
			if !newProof {
				updatedAt = previous
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO racing_candidates(candidate_key,site_id,event_key,source_scope,first_seen_at,candidate_json,selection_json,state,next_evaluation_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(candidate_key) DO UPDATE SET candidate_json=excluded.candidate_json,selection_json=excluded.selection_json,state=excluded.state,next_evaluation_at=excluded.next_evaluation_at,updated_at=excluded.updated_at`, record.Key, record.SiteID, record.EventKey, record.SourceScope, record.FirstSeenAt, string(record.Candidate), string(record.Selection), record.State, record.NextEvaluationAt, updatedAt)
		if err != nil {
			return 0, err
		}
		for _, item := range observed {
			if _, err := tx.ExecContext(ctx, `UPDATE racing_discoveries SET evaluated_revision=? WHERE id=? AND revision=?`, item.Revision, item.ID, item.Revision); err != nil {
				return 0, err
			}
		}
		return 0, nil
	})
	return err
}

// CandidateRecords supports bounded configuration rescans and time-based
// reevaluation. Passing dueBefore selects only scheduled records; nil scans keys.
func (s *RacingStore) CandidateRecords(ctx context.Context, after string, dueBefore *string, limit int) ([]RacingCandidateRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	query := `SELECT candidate_key,site_id,event_key,source_scope,first_seen_at,candidate_json,selection_json,state,next_evaluation_at,updated_at FROM racing_candidates WHERE candidate_key>?`
	args := []any{after}
	if dueBefore != nil {
		query += " AND next_evaluation_at<=?"
		args = append(args, *dueBefore)
	}
	query += " ORDER BY candidate_key LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RacingCandidateRecord{}
	for rows.Next() {
		var record RacingCandidateRecord
		var candidate, selection string
		if err := rows.Scan(&record.Key, &record.SiteID, &record.EventKey, &record.SourceScope, &record.FirstSeenAt, &candidate, &selection, &record.State, &record.NextEvaluationAt, &record.UpdatedAt); err != nil {
			return nil, err
		}
		record.Candidate = json.RawMessage(candidate)
		record.Selection = json.RawMessage(selection)
		result = append(result, record)
	}
	return result, rows.Err()
}

// ExecutableCandidates orders all ready events before paging, so an official
// candidate cannot sit behind an earlier ordinary candidate-key page. Offsets
// are scan-local; the next scheduler tick starts a new scan after concurrent edits.
func (s *RacingStore) ExecutableCandidates(ctx context.Context, offset, limit int) ([]RacingCandidateRecord, error) {
	if offset < 0 {
		offset = 0
	}
	if limit < 1 || limit > 100 {
		limit = 100
	}
	priority := `json_extract(selection_json,'$.priority')`
	if dbinterface.DialectOf(s.db) == "postgres" {
		priority = `(selection_json::jsonb->>'priority')`
	}
	query := `SELECT candidate_key,site_id,event_key,source_scope,first_seen_at,candidate_json,selection_json,state,next_evaluation_at,updated_at FROM racing_candidates WHERE state='ready' ORDER BY CASE WHEN ` + priority + `='official' THEN 0 ELSE 1 END,first_seen_at,candidate_key LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RacingCandidateRecord{}
	for rows.Next() {
		var record RacingCandidateRecord
		var candidate, selection string
		if err := rows.Scan(&record.Key, &record.SiteID, &record.EventKey, &record.SourceScope, &record.FirstSeenAt, &candidate, &selection, &record.State, &record.NextEvaluationAt, &record.UpdatedAt); err != nil {
			return nil, err
		}
		record.Candidate = json.RawMessage(candidate)
		record.Selection = json.RawMessage(selection)
		result = append(result, record)
	}
	return result, rows.Err()
}
