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

// Runtime source secrets are deliberately excluded even from accidental JSON
// serialization. Only the source observer may use this projection.
type RacingRuntimeSource struct {
	SecretError bool `json:"-"`
	RacingSource
	SiteOrigin             string `json:"-"`
	Cookie                 string `json:"-"`
	URL                    string `json:"-"`
	SiteRevision           string `json:"-"`
	RequestIntervalSeconds int    `json:"-"`
}

type RacingDiscovery struct {
	ID          int64           `json:"id"`
	SourceID    int             `json:"sourceId"`
	SiteID      int             `json:"siteId"`
	EventKey    string          `json:"eventKey"`
	Item        json.RawMessage `json:"item"`
	Eligible    bool            `json:"eligible"`
	FirstSeenAt string          `json:"firstSeenAt"`
	LastSeenAt  string          `json:"lastSeenAt"`
	Revision    int64           `json:"revision"`
}

func (s *RacingStore) openRacingSecret(encoded, purpose string) (string, error) {
	encrypted, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("invalid racing secret")
	}
	plain, err := s.secrets.Open(nil, nil, encrypted, []byte(purpose))
	if err != nil {
		return "", errors.New("racing secret decryption failed")
	}
	return string(plain), nil
}

func (s *RacingStore) RuntimeSources(ctx context.Context) ([]RacingRuntimeSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source.id,source.site_id,source.name,source.kind,source.enabled,source.interval_seconds,source.url_origin,source.updated_at,source.adapter,source.page_count,source.initial_lookback_seconds,
 site.base_url,site.updated_at,site.request_interval_seconds,endpoint.ciphertext,COALESCE(credential.ciphertext,'')
 FROM racing_sources source JOIN racing_sites site ON site.id=source.site_id
 JOIN racing_secrets endpoint ON endpoint.id=source.endpoint_id LEFT JOIN racing_secrets credential ON credential.id=site.credential_id
 WHERE source.enabled=1 AND site.enabled=1 ORDER BY source.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RacingRuntimeSource{}
	for rows.Next() {
		var input RacingRuntimeSource
		var endpoint, credential string
		if err := rows.Scan(&input.ID, &input.SiteID, &input.Name, &input.Kind, &input.Enabled, &input.IntervalSeconds, &input.URLOrigin, &input.UpdatedAt, &input.Adapter, &input.PageCount, &input.InitialLookbackSeconds, &input.SiteOrigin, &input.SiteRevision, &input.RequestIntervalSeconds, &endpoint, &credential); err != nil {
			return nil, err
		}
		input.URL, err = s.openRacingSecret(endpoint, "source-url")
		if err != nil {
			input.SecretError = true
		}
		if credential != "" {
			input.Cookie, err = s.openRacingSecret(credential, "site-credential")
			if err != nil {
				input.SecretError = true
			}
		}
		result = append(result, input)
	}
	return result, rows.Err()
}

// Baseline is established before the first request and survives partial scans
// and process restarts. A restart must never make old entries appear new.
func (s *RacingStore) SourceBaseline(ctx context.Context, id, lookbackSeconds int) (time.Time, error) {
	now := time.Now().UTC().Add(-time.Duration(lookbackSeconds) * time.Second).Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO racing_source_cursors(source_id,baseline_at) VALUES (?,?) ON CONFLICT(source_id) DO NOTHING`, id, now); err != nil {
		return time.Time{}, err
	}
	var value string
	if err := s.db.QueryRowContext(ctx, `SELECT baseline_at FROM racing_source_cursors WHERE source_id=?`, id).Scan(&value); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, value)
}

// Each item is its own transaction. Later page failures cannot roll it back.
// Configuration revisions prevent canceled or superseded workers from writing.
func (s *RacingStore) SaveDiscovery(ctx context.Context, source RacingRuntimeSource, event string, public, private []byte, eligible bool) error {
	if event == "" || len(event) > 300 || !json.Valid(public) || !json.Valid(private) {
		return errors.New("invalid racing observation")
	}
	ciphertext := base64.RawStdEncoding.EncodeToString(s.secrets.Seal(nil, nil, private, []byte("source-discovery")))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		query := `SELECT source.id FROM racing_sources source JOIN racing_sites site ON site.id=source.site_id WHERE source.id=? AND source.updated_at=? AND site.updated_at=? AND source.enabled=1 AND site.enabled=1`
		if dbinterface.DialectOf(s.db) == "postgres" {
			query += " FOR SHARE OF source, site"
		}
		var currentID int
		if err := tx.QueryRowContext(ctx, query, source.ID, source.UpdatedAt, source.SiteRevision).Scan(&currentID); err != nil {
			return 0, err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO racing_discoveries(source_id,site_id,event_key,public_item,private_ciphertext,eligible,first_seen_at,last_seen_at) VALUES (?,?,?,?,?,?,?,?)
 ON CONFLICT(source_id,site_id,event_key) DO UPDATE SET public_item=excluded.public_item,private_ciphertext=excluded.private_ciphertext,eligible=excluded.eligible,last_seen_at=excluded.last_seen_at,
 revision=racing_discoveries.revision+CASE WHEN racing_discoveries.public_item<>excluded.public_item OR racing_discoveries.eligible<>excluded.eligible THEN 1 ELSE 0 END`, source.ID, source.SiteID, event, string(public), ciphertext, boolToInt(eligible), now, now)
		return 0, err
	})
	return err
}

func (s *RacingStore) CompleteSourceScan(ctx context.Context, source RacingRuntimeSource) error {
	result, err := s.db.ExecContext(ctx, `UPDATE racing_source_cursors SET last_success_at=? WHERE source_id=? AND EXISTS (SELECT 1 FROM racing_sources source JOIN racing_sites site ON site.id=source.site_id WHERE source.id=? AND source.updated_at=? AND site.updated_at=? AND source.enabled=1 AND site.enabled=1)`, time.Now().UTC().Format(time.RFC3339Nano), source.ID, source.ID, source.UpdatedAt, source.SiteRevision)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected != 1 {
		return errors.New("source configuration changed")
	}
	return err
}

// Bounded recent observations for the API. Private transport payloads never
// leave this store through an API model.
func (s *RacingStore) Discoveries(ctx context.Context, limit int) ([]RacingDiscovery, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,source_id,site_id,event_key,public_item,eligible,first_seen_at,last_seen_at,revision FROM racing_discoveries ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return readDiscoveries(rows)
}

// PrivateDiscovery is reserved for later metadata verification. Callers must
// not serialize or log the returned transport payload.
func (s *RacingStore) PrivateDiscovery(ctx context.Context, id int64) ([]byte, error) {
	var ciphertext string
	if err := s.db.QueryRowContext(ctx, `SELECT private_ciphertext FROM racing_discoveries WHERE id=?`, id).Scan(&ciphertext); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}
	plain, err := s.openRacingSecret(ciphertext, "source-discovery")
	return []byte(plain), err
}
