// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
)

func (s *RacingStore) CandidateMetadata(ctx context.Context, key string) (json.RawMessage, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT public_metadata FROM racing_candidate_metadata WHERE candidate_key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return json.RawMessage(value), err
}

func (s *RacingStore) SaveCandidateMetadata(ctx context.Context, key string, source RacingRuntimeSource, public, payload []byte) error {
	if !json.Valid(public) || len(payload) == 0 {
		return errors.New("invalid candidate metadata")
	}
	encrypted := base64.RawStdEncoding.EncodeToString(s.secrets.Seal(nil, nil, payload, []byte("candidate-metainfo:"+key)))
	_, err := s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		query := `SELECT source.id FROM racing_sources source JOIN racing_sites site ON site.id=source.site_id WHERE source.id=? AND source.updated_at=? AND site.updated_at=? AND source.enabled=1 AND site.enabled=1`
		if dbinterface.DialectOf(s.db) == "postgres" {
			query += " FOR SHARE OF source, site"
		}
		var current int
		if err := tx.QueryRowContext(ctx, query, source.ID, source.UpdatedAt, source.SiteRevision).Scan(&current); err != nil {
			return 0, err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO racing_candidate_metadata(candidate_key,source_id,public_metadata,private_ciphertext,observed_at) VALUES (?,?,?,?,?) ON CONFLICT(candidate_key) DO UPDATE SET source_id=excluded.source_id,public_metadata=excluded.public_metadata,private_ciphertext=excluded.private_ciphertext,observed_at=excluded.observed_at`, key, source.ID, string(public), encrypted, time.Now().UTC().Format(time.RFC3339Nano))
		return 0, err
	})
	return err
}
