// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
)

var ErrRacingInvalid = errors.New("invalid racing configuration")
var ErrRacingReferenced = errors.New("racing configuration is still referenced")

type RacingStore struct {
	db      dbinterface.Querier
	secrets cipher.AEAD
}

func NewRacingStore(db dbinterface.Querier, key []byte) (*RacingStore, error) {
	if len(key) != 32 {
		return nil, errors.New("racing encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return &RacingStore{db: db, secrets: aead}, nil
}

func racingInvalid(field string) error { return fmt.Errorf("%w: %s", ErrRacingInvalid, field) }

func racingName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 200 {
		return "", racingInvalid("name must contain 1 to 200 bytes")
	}
	return value, nil
}

func racingURL(raw string, originOnly bool) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Fragment != "" {
		return "", "", racingInvalid("URL must be an HTTP or HTTPS address without a fragment")
	}
	origin := parsed.Scheme + "://" + parsed.Host
	if originOnly && (parsed.User != nil || parsed.RawQuery != "" || (parsed.Path != "" && parsed.Path != "/")) {
		return "", "", racingInvalid("site base URL must contain only scheme and host")
	}
	return parsed.String(), origin, nil
}

func racingIDs(ids []int) ([]int, error) {
	result := slices.Clone(ids)
	if result == nil {
		result = []int{}
	}
	slices.Sort(result)
	for _, id := range result {
		if id <= 0 {
			return nil, racingInvalid("references must be positive IDs")
		}
	}
	return slices.Compact(result), nil
}

// Only these fixed internal identifiers may be interpolated into store queries.
func racingTable(kind string) (string, error) {
	switch kind {
	case "storage-pools":
		return "racing_storage_pools", nil
	case "path-mappings":
		return "racing_path_mappings", nil
	case "sites":
		return "racing_sites", nil
	case "sources":
		return "racing_sources", nil
	case "groups":
		return "racing_groups", nil
	case "rules":
		return "racing_rules", nil
	default:
		return "", racingInvalid("unknown resource")
	}
}

func racingExists(ctx context.Context, tx dbinterface.TxQuerier, table string, id int) error {
	var found int
	err := tx.QueryRowContext(ctx, "SELECT id FROM "+table+" WHERE id = ?", id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return racingInvalid("referenced object does not exist")
	}
	return err
}

func (s *RacingStore) write(ctx context.Context, fn func(dbinterface.TxQuerier) (int, error)) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	id, err := fn(tx)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func racingUpdated(ctx context.Context, tx dbinterface.TxQuerier, query string, args ...any) error {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *RacingStore) saveSecret(ctx context.Context, tx dbinterface.TxQuerier, id *int, value, purpose string) (int, error) {
	ciphertext := base64.RawStdEncoding.EncodeToString(s.secrets.Seal(nil, nil, []byte(value), []byte(purpose)))
	if id != nil {
		return *id, racingUpdated(ctx, tx, "UPDATE racing_secrets SET ciphertext = ? WHERE id = ?", ciphertext, *id)
	}
	var created int
	err := tx.QueryRowContext(ctx, "INSERT INTO racing_secrets(ciphertext) VALUES (?) RETURNING id", ciphertext).Scan(&created)
	return created, err
}

func (s *RacingStore) SaveSite(ctx context.Context, id int, input RacingSiteInput) (int, error) {
	name, err := racingName(input.Name)
	if err != nil {
		return 0, err
	}
	_, origin, err := racingURL(input.BaseURL, true)
	if err != nil {
		return 0, err
	}
	if input.RequestIntervalSeconds <= 0 || input.RequestIntervalSeconds > 86400 {
		return 0, racingInvalid("request interval must be between 1 and 86400 seconds")
	}
	hosts := make([]string, 0, len(input.TrackerHosts))
	for _, host := range input.TrackerHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || strings.ContainsAny(host, "/?:@# \\ \t\r\n") {
			return 0, racingInvalid("tracker hosts must be bare hostnames")
		}
		hosts = append(hosts, host)
	}
	slices.Sort(hosts)
	hosts = slices.Compact(hosts)
	encoded, _ := json.Marshal(hosts)
	return s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		var secretID *int
		if id > 0 {
			if err := tx.QueryRowContext(ctx, "SELECT credential_id FROM racing_sites WHERE id = ?", id).Scan(&secretID); err != nil {
				return 0, err
			}
		}
		previousSecret := secretID
		if input.Credential != nil {
			if *input.Credential == "" {
				secretID = nil
			} else {
				saved, err := s.saveSecret(ctx, tx, secretID, *input.Credential, "site-credential")
				if err != nil {
					return 0, err
				}
				secretID = &saved
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if id == 0 {
			err := tx.QueryRowContext(ctx, `INSERT INTO racing_sites(name,base_url,enabled,tracker_hosts,request_interval_seconds,credential_id,updated_at) VALUES (?,?,?,?,?,?,?) RETURNING id`, name, origin, boolToInt(input.Enabled), string(encoded), input.RequestIntervalSeconds, secretID, now).Scan(&id)
			if err != nil {
				return 0, err
			}
		} else if err := racingUpdated(ctx, tx, `UPDATE racing_sites SET name=?,base_url=?,enabled=?,tracker_hosts=?,request_interval_seconds=?,credential_id=?,updated_at=? WHERE id=?`, name, origin, boolToInt(input.Enabled), string(encoded), input.RequestIntervalSeconds, secretID, now, id); err != nil {
			return 0, err
		}
		if previousSecret != nil && secretID == nil {
			if _, err := tx.ExecContext(ctx, "DELETE FROM racing_secrets WHERE id=?", *previousSecret); err != nil {
				return 0, err
			}
		}
		return id, nil
	})
}

func (s *RacingStore) SaveSource(ctx context.Context, id int, input RacingSourceInput) (int, error) {
	name, err := racingName(input.Name)
	if err != nil {
		return 0, err
	}
	if input.SiteID <= 0 || input.IntervalSeconds <= 0 || input.IntervalSeconds > 86400 {
		return 0, racingInvalid("site ID and interval (1 to 86400 seconds) are required")
	}
	if !slices.Contains([]string{"rss", "web", "revival"}, input.Kind) {
		return 0, racingInvalid("source kind")
	}
	if input.Adapter == "" && input.Kind == "rss" {
		input.Adapter = "generic-rss"
	}
	if input.Adapter != "" && input.Adapter != "chd" && (input.Kind != "rss" || !slices.Contains([]string{"generic-rss", "mteam"}, input.Adapter)) {
		return 0, racingInvalid("adapter does not support source kind")
	}
	if input.PageCount == 0 {
		input.PageCount = 1
	}
	if input.PageCount < 1 || input.PageCount > 5 || input.InitialLookbackSeconds < 0 || input.InitialLookbackSeconds > 604800 {
		return 0, racingInvalid("page count or initial lookback")
	}
	var endpoint, origin string
	if input.URL != nil {
		endpoint, origin, err = racingURL(*input.URL, false)
		if err != nil {
			return 0, err
		}
	}
	if id == 0 && input.URL == nil {
		return 0, racingInvalid("source URL is required")
	}
	return s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := racingExists(ctx, tx, "racing_sites", input.SiteID); err != nil {
			return 0, err
		}
		var secretID *int
		if id > 0 {
			var storedOrigin string
			if err := tx.QueryRowContext(ctx, "SELECT endpoint_id,url_origin FROM racing_sources WHERE id=?", id).Scan(&secretID, &storedOrigin); err != nil {
				return 0, err
			}
			if input.URL == nil {
				origin = storedOrigin
			}
		}
		if input.URL != nil {
			saved, err := s.saveSecret(ctx, tx, secretID, endpoint, "source-url")
			if err != nil {
				return 0, err
			}
			secretID = &saved
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if id == 0 {
			err := tx.QueryRowContext(ctx, `INSERT INTO racing_sources(site_id,name,kind,enabled,interval_seconds,endpoint_id,url_origin,updated_at,adapter,page_count,initial_lookback_seconds) VALUES (?,?,?,?,?,?,?,?,?,?,?) RETURNING id`, input.SiteID, name, input.Kind, boolToInt(input.Enabled), input.IntervalSeconds, secretID, origin, now, input.Adapter, input.PageCount, input.InitialLookbackSeconds).Scan(&id)
			return id, err
		}
		return id, racingUpdated(ctx, tx, `UPDATE racing_sources SET site_id=?,name=?,kind=?,enabled=?,interval_seconds=?,endpoint_id=?,url_origin=?,updated_at=?,adapter=?,page_count=?,initial_lookback_seconds=? WHERE id=?`, input.SiteID, name, input.Kind, boolToInt(input.Enabled), input.IntervalSeconds, secretID, origin, now, input.Adapter, input.PageCount, input.InitialLookbackSeconds, id)
	})
}

func (s *RacingStore) SaveGroup(ctx context.Context, id int, input RacingGroupInput) (int, error) {
	name, err := racingName(input.Name)
	if err != nil {
		return 0, err
	}
	members, err := racingIDs(input.InstanceIDs)
	if err != nil {
		return 0, err
	}
	return s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		for _, member := range members {
			if err := racingExists(ctx, tx, "instances", member); err != nil {
				return 0, err
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if id == 0 {
			if err := tx.QueryRowContext(ctx, `INSERT INTO racing_groups(name,enabled,updated_at) VALUES (?,?,?) RETURNING id`, name, boolToInt(input.Enabled), now).Scan(&id); err != nil {
				return 0, err
			}
		} else if err := racingUpdated(ctx, tx, `UPDATE racing_groups SET name=?,enabled=?,updated_at=? WHERE id=?`, name, boolToInt(input.Enabled), now, id); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM racing_group_members WHERE group_id=?", id); err != nil {
			return 0, err
		}
		for _, member := range members {
			if _, err := tx.ExecContext(ctx, "INSERT INTO racing_group_members(group_id,instance_id) VALUES (?,?)", id, member); err != nil {
				return 0, err
			}
		}
		return id, nil
	})
}

func (s *RacingStore) SaveRule(ctx context.Context, id int, input RacingRuleInput) (int, error) {
	name, err := racingName(input.Name)
	if err != nil {
		return 0, err
	}
	sources, err := racingIDs(input.SourceIDs)
	if err != nil {
		return 0, err
	}
	if len(sources) == 0 {
		return 0, racingInvalid("at least one source is required")
	}
	if (input.TargetGroupID == nil) == (input.TargetInstanceID == nil) {
		return 0, racingInvalid("choose exactly one target group or instance")
	}
	if input.ReceiveWindowSeconds <= 0 || int64(input.ReceiveWindowSeconds) > int64((1<<63-1)/time.Second) {
		return 0, racingInvalid("receive window must be an explicit positive duration")
	}
	kinds := slices.Clone(input.AcceptKinds)
	slices.Sort(kinds)
	kinds = slices.Compact(kinds)
	if len(kinds) == 0 {
		return 0, racingInvalid("at least one accepted kind is required")
	}
	for _, kind := range kinds {
		if !slices.Contains([]string{"official", "free", "revival"}, kind) {
			return 0, racingInvalid("accepted kind")
		}
	}
	f := input.Filters
	if (f.MinSizeBytes != nil && *f.MinSizeBytes < 0) || (f.MaxSizeBytes != nil && *f.MaxSizeBytes < 0) || (f.MinSizeBytes != nil && f.MaxSizeBytes != nil && *f.MinSizeBytes > *f.MaxSizeBytes) {
		return 0, racingInvalid("size bounds")
	}
	for _, keyword := range append(slices.Clone(f.IncludeKeywords), f.ExcludeKeywords...) {
		if strings.TrimSpace(keyword) == "" {
			return 0, racingInvalid("keywords must not be blank")
		}
	}
	if f.IncludeKeywords == nil {
		f.IncludeKeywords = []string{}
	}
	if f.ExcludeKeywords == nil {
		f.ExcludeKeywords = []string{}
	}
	encodedKinds, _ := json.Marshal(kinds)
	encodedFilters, _ := json.Marshal(f)
	return s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		for _, source := range sources {
			if err := racingExists(ctx, tx, "racing_sources", source); err != nil {
				return 0, err
			}
		}
		if input.TargetGroupID != nil {
			if err := racingExists(ctx, tx, "racing_groups", *input.TargetGroupID); err != nil {
				return 0, err
			}
		}
		if input.TargetInstanceID != nil {
			if err := racingExists(ctx, tx, "instances", *input.TargetInstanceID); err != nil {
				return 0, err
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if id == 0 {
			err := tx.QueryRowContext(ctx, `INSERT INTO racing_rules(name,enabled,sort_order,accept_kinds,filters,receive_window_seconds,target_group_id,target_instance_id,allow_official_reclaim,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?) RETURNING id`, name, boolToInt(input.Enabled), input.SortOrder, string(encodedKinds), string(encodedFilters), input.ReceiveWindowSeconds, input.TargetGroupID, input.TargetInstanceID, boolToInt(input.AllowOfficialReclaim), now).Scan(&id)
			if err != nil {
				return 0, err
			}
		} else if err := racingUpdated(ctx, tx, `UPDATE racing_rules SET name=?,enabled=?,sort_order=?,accept_kinds=?,filters=?,receive_window_seconds=?,target_group_id=?,target_instance_id=?,allow_official_reclaim=?,updated_at=? WHERE id=?`, name, boolToInt(input.Enabled), input.SortOrder, string(encodedKinds), string(encodedFilters), input.ReceiveWindowSeconds, input.TargetGroupID, input.TargetInstanceID, boolToInt(input.AllowOfficialReclaim), now, id); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM racing_rule_sources WHERE rule_id=?", id); err != nil {
			return 0, err
		}
		for _, source := range sources {
			if _, err := tx.ExecContext(ctx, "INSERT INTO racing_rule_sources(rule_id,source_id) VALUES (?,?)", id, source); err != nil {
				return 0, err
			}
		}
		return id, nil
	})
}

func (s *RacingStore) Delete(ctx context.Context, kind string, id int) error {
	table, err := racingTable(kind)
	if err != nil {
		return err
	}
	_, err = s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		var referenceQuery string
		switch kind {
		case "storage-pools":
			referenceQuery = "SELECT count(*) FROM racing_path_mappings WHERE storage_pool_id=?"
		case "sites":
			referenceQuery = "SELECT count(*) FROM racing_sources WHERE site_id=?"
		case "sources":
			referenceQuery = "SELECT count(*) FROM racing_rule_sources WHERE source_id=?"
		case "groups":
			referenceQuery = "SELECT count(*) FROM racing_rules WHERE target_group_id=?"
		}
		if referenceQuery != "" {
			var count int
			if err := tx.QueryRowContext(ctx, referenceQuery, id).Scan(&count); err != nil {
				return 0, err
			}
			if count > 0 {
				return 0, ErrRacingReferenced
			}
		}
		var secretID *int
		switch kind {
		case "sites":
			err = tx.QueryRowContext(ctx, "SELECT credential_id FROM racing_sites WHERE id=?", id).Scan(&secretID)
		case "sources":
			err = tx.QueryRowContext(ctx, "SELECT endpoint_id FROM racing_sources WHERE id=?", id).Scan(&secretID)
		}
		if err != nil {
			return 0, err
		}
		if err := racingUpdated(ctx, tx, "DELETE FROM "+table+" WHERE id=?", id); err != nil {
			return 0, err
		}
		if secretID != nil {
			if _, err := tx.ExecContext(ctx, "DELETE FROM racing_secrets WHERE id=?", *secretID); err != nil {
				return 0, err
			}
		}
		return id, nil
	})
	return err
}
