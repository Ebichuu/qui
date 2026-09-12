// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"encoding/json"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
)

// These records explain a read-only evaluation. They are neither reservations
// nor execution plans, and do not grant deletion ownership or budget credits.
type RacingReclaimAssessment struct {
	CandidateKey          string          `json:"candidateKey"`
	InstanceID            int             `json:"instanceId"`
	ConfigurationRevision int64           `json:"configurationRevision"`
	Assessment            json.RawMessage `json:"assessment"`
	ObservedAt            string          `json:"observedAt"`
}

func (s *RacingStore) SaveReclaimAssessment(ctx context.Context, item RacingReclaimAssessment) error {
	if item.CandidateKey == "" || item.InstanceID <= 0 || !json.Valid(item.Assessment) {
		return ErrRacingInvalid
	}
	if _, err := time.Parse(time.RFC3339Nano, item.ObservedAt); err != nil {
		return ErrRacingInvalid
	}
	_, err := s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := s.lockExecution(ctx, tx, &item.ConfigurationRevision); err != nil {
			return 0, err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO racing_reclaim_assessments(candidate_key,instance_id,configuration_revision,assessment_json,observed_at) VALUES(?,?,?,?,?) ON CONFLICT(candidate_key,instance_id) DO UPDATE SET configuration_revision=excluded.configuration_revision,assessment_json=excluded.assessment_json,observed_at=excluded.observed_at`, item.CandidateKey, item.InstanceID, item.ConfigurationRevision, string(item.Assessment), item.ObservedAt)
		return 0, err
	})
	return err
}

func (s *RacingStore) ReclaimAssessments(ctx context.Context, limit int) ([]RacingReclaimAssessment, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT candidate_key,instance_id,configuration_revision,assessment_json,observed_at FROM racing_reclaim_assessments ORDER BY observed_at DESC,candidate_key,instance_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RacingReclaimAssessment{}
	for rows.Next() {
		var item RacingReclaimAssessment
		var raw string
		if err := rows.Scan(&item.CandidateKey, &item.InstanceID, &item.ConfigurationRevision, &raw, &item.ObservedAt); err != nil {
			return nil, err
		}
		item.Assessment = json.RawMessage(raw)
		result = append(result, item)
	}
	return result, rows.Err()
}
