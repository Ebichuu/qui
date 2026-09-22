// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const RacingAnalysisWindow = 20 * time.Minute
const RacingAnalysisInterval = 5 * time.Second
const RacingAnalysisPeerLimit = 1000
const RacingAnalysisRetention = 30 * 24 * time.Hour

type RacingAnalysisSetting struct {
	InstanceID int  `json:"instanceId"`
	Enabled    bool `json:"enabled"`
}

type RacingPeerHistory struct {
	ASN           *RacingASNObservation `json:"asn,omitempty"`
	IP            string                `json:"ip"`
	Port          int                   `json:"port"`
	FirstSeen     time.Time             `json:"firstSeen"`
	LastSeen      time.Time             `json:"lastSeen"`
	AbsentAt      *time.Time            `json:"absentAt,omitempty"`
	Samples       int                   `json:"samples"`
	MaxProgress   *float64              `json:"maxProgress,omitempty"`
	FirstComplete *time.Time            `json:"firstComplete,omitempty"`
	Downloaded    int64                 `json:"downloaded"`
	Uploaded      int64                 `json:"uploaded"`
	CounterResets int                   `json:"counterResets"`
}

type RacingASNObservation struct {
	State           string    `json:"state"`
	Number          uint32    `json:"number,omitempty"`
	Organization    string    `json:"organization,omitempty"`
	Network         string    `json:"network,omitempty"`
	ObservedAt      time.Time `json:"observedAt"`
	DatabaseBuiltAt time.Time `json:"databaseBuiltAt"`
	DatabaseSHA256  string    `json:"databaseSHA256"`
}

type RacingAnalysisSample struct {
	At    time.Time `json:"at"`
	State string    `json:"state"`
}

type RacingAnalysisHistory struct {
	CandidateKey      string                       `json:"candidateKey"`
	InstanceID        int                          `json:"instanceId"`
	Hash              string                       `json:"hash"`
	HashV1            string                       `json:"hashV1"`
	HashV2            string                       `json:"hashV2"`
	AddedOn           int64                        `json:"addedOn"`
	StartedAt         time.Time                    `json:"startedAt"`
	LastVisible       *time.Time                   `json:"lastVisible,omitempty"`
	LastSuccess       *time.Time                   `json:"lastSuccess,omitempty"`
	Samples           []RacingAnalysisSample       `json:"samples"`
	Peers             map[string]RacingPeerHistory `json:"peers"`
	Truncated         bool                         `json:"truncated"`
	ExpectedSamples   int                          `json:"expectedSamples"`
	SuccessfulSamples int                          `json:"successfulSamples"`
	MissingSamples    int                          `json:"missingSamples"`
	ASNState          string                       `json:"asnState"`
	RankingState      string                       `json:"rankingState"`
}

func (h *RacingAnalysisHistory) Coverage(now time.Time) {
	elapsed := min(max(now.Sub(h.StartedAt), 0), RacingAnalysisWindow)
	h.ExpectedSamples = int((elapsed + RacingAnalysisInterval - 1) / RacingAnalysisInterval)
	h.SuccessfulSamples = 0
	for _, sample := range h.Samples {
		if sample.State == "observed" {
			h.SuccessfulSamples++
		}
	}
	h.MissingSamples = max(h.ExpectedSamples-h.SuccessfulSamples, 0)
	h.ASNState, h.RankingState = "unavailable", "unsupported"
	resolved := 0
	for _, peer := range h.Peers {
		if peer.ASN != nil && peer.ASN.State != "error" {
			resolved++
		}
	}
	if resolved > 0 {
		h.ASNState = "partial"
		if resolved == len(h.Peers) {
			h.ASNState = "available"
		}
	}
}

func (s *RacingStore) AnalysisSettings(ctx context.Context) ([]RacingAnalysisSetting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,enabled FROM racing_analysis_settings ORDER BY instance_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RacingAnalysisSetting{}
	for rows.Next() {
		var item RacingAnalysisSetting
		if err := rows.Scan(&item.InstanceID, &item.Enabled); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *RacingStore) SaveAnalysisSetting(ctx context.Context, input RacingAnalysisSetting) error {
	if input.InstanceID <= 0 {
		return racingInvalid("analysis instance")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO racing_analysis_settings(instance_id,enabled) VALUES(?,?) ON CONFLICT(instance_id) DO UPDATE SET enabled=excluded.enabled`, input.InstanceID, boolToInt(input.Enabled))
	return err
}

// AnalysisNext chooses one least recently attempted eligible intent. This read
// never reserves capacity or changes the immutable reception decision.
func (s *RacingStore) AnalysisNext(ctx context.Context, now time.Time) (*RacingAddIntent, error) {
	var key string
	err := s.db.QueryRowContext(ctx, `SELECT i.candidate_key FROM racing_add_intents i JOIN racing_analysis_settings p ON p.instance_id=i.instance_id LEFT JOIN racing_analysis_history h ON h.candidate_key=i.candidate_key WHERE p.enabled=1 AND i.confirmed_at>? AND i.confirmed_at<=? AND (h.updated_at IS NULL OR h.updated_at<?) ORDER BY COALESCE(h.updated_at,''),i.confirmed_at,i.candidate_key LIMIT 1`, now.Add(-RacingAnalysisWindow).UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), now.Add(-RacingAnalysisInterval).UTC().Format(time.RFC3339Nano)).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	intent, err := s.AddIntent(ctx, key)
	return &intent, err
}

func (s *RacingStore) AnalysisHistory(ctx context.Context, key string) (*RacingAnalysisHistory, error) {
	now := time.Now()
	var raw, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT report_json,updated_at FROM racing_analysis_history WHERE candidate_key=?`, key).Scan(&raw, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		intent, intentErr := s.AddIntent(ctx, key)
		if errors.Is(intentErr, sql.ErrNoRows) {
			return nil, nil
		}
		if intentErr != nil {
			return nil, intentErr
		}
		if intent.ConfirmedAt == nil {
			return nil, nil
		}
		started, parseErr := time.Parse(time.RFC3339Nano, *intent.ConfirmedAt)
		if parseErr != nil {
			return nil, parseErr
		}
		if now.Sub(started) > RacingAnalysisRetention {
			return nil, nil
		}
		// Durable confirmed intents remain the denominator even if no worker
		// ran during the entire window. Never substitute an empty success.
		item := RacingAnalysisHistory{CandidateKey: key, InstanceID: intent.InstanceID, HashV1: intent.Plan.HashV1, HashV2: intent.Plan.HashV2, StartedAt: started, Samples: []RacingAnalysisSample{}, Peers: map[string]RacingPeerHistory{}}
		item.Coverage(now)
		return &item, nil
	}
	if err != nil {
		return nil, err
	}
	updated, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return nil, err
	}
	// Cleanup is bounded and may lag after downtime. Do not expose expired
	// endpoint data while waiting for the background deletion batch.
	if now.Sub(updated) > RacingAnalysisRetention {
		return nil, nil
	}
	var item RacingAnalysisHistory
	if err = json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, err
	}
	item.Coverage(now)
	return &item, nil
}

func (s *RacingStore) SaveAnalysisHistory(ctx context.Context, item RacingAnalysisHistory) error {
	if item.CandidateKey == "" || len(item.Samples) == 0 || len(item.Samples) > 240 || len(item.Peers) > RacingAnalysisPeerLimit {
		return racingInvalid("bounded analysis history")
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	// The sole analysis worker owns aggregation. Timestamp CAS also prevents an
	// older response from overwriting history saved by another process.
	_, err = s.db.ExecContext(ctx, `INSERT INTO racing_analysis_history(candidate_key,report_json,updated_at) VALUES(?,?,?) ON CONFLICT(candidate_key) DO UPDATE SET report_json=excluded.report_json,updated_at=excluded.updated_at WHERE racing_analysis_history.updated_at<excluded.updated_at`, item.CandidateKey, string(raw), item.Samples[len(item.Samples)-1].At.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *RacingStore) PruneAnalysisHistory(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM racing_analysis_history WHERE candidate_key IN (SELECT candidate_key FROM racing_analysis_history WHERE updated_at<? ORDER BY updated_at LIMIT 100)`, now.Add(-RacingAnalysisRetention).UTC().Format(time.RFC3339Nano))
	return err
}
