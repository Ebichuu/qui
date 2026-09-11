// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Configuration returns one consistent, redacted configuration snapshot.
// Secret IDs and ciphertext never enter API-facing models.
func (s *RacingStore) Configuration(ctx context.Context) (*RacingConfiguration, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result := &RacingConfiguration{Sites: []RacingSite{}, Sources: []RacingSource{}, Groups: []RacingGroup{}, Rules: []RacingRule{}}
	if err := tx.QueryRowContext(ctx, "SELECT revision FROM racing_configuration_revision WHERE id=1").Scan(&result.Revision); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,name,base_url,enabled,tracker_hosts,request_interval_seconds,credential_id IS NOT NULL,updated_at FROM racing_sites ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var site RacingSite
		var trackers string
		if err := rows.Scan(&site.ID, &site.Name, &site.BaseURL, &site.Enabled, &trackers, &site.RequestIntervalSeconds, &site.HasCredential, &site.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(trackers), &site.TrackerHosts); err != nil {
			rows.Close()
			return nil, err
		}
		result.Sites = append(result.Sites, site)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT id,site_id,name,kind,enabled,interval_seconds,url_origin,updated_at,adapter,page_count,initial_lookback_seconds FROM racing_sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var source RacingSource
		if err := rows.Scan(&source.ID, &source.SiteID, &source.Name, &source.Kind, &source.Enabled, &source.IntervalSeconds, &source.URLOrigin, &source.UpdatedAt, &source.Adapter, &source.PageCount, &source.InitialLookbackSeconds); err != nil {
			rows.Close()
			return nil, err
		}
		result.Sources = append(result.Sources, source)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT id,name,enabled,updated_at FROM racing_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	groups := make(map[int]int)
	for rows.Next() {
		var group RacingGroup
		if err := rows.Scan(&group.ID, &group.Name, &group.Enabled, &group.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		group.InstanceIDs = []int{}
		groups[group.ID] = len(result.Groups)
		result.Groups = append(result.Groups, group)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT group_id,instance_id FROM racing_group_members ORDER BY group_id,instance_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var group, instance int
		if err := rows.Scan(&group, &instance); err != nil {
			rows.Close()
			return nil, err
		}
		index := groups[group]
		result.Groups[index].InstanceIDs = append(result.Groups[index].InstanceIDs, instance)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT id,name,enabled,sort_order,accept_kinds,filters,receive_window_seconds,target_group_id,target_instance_id,allow_official_reclaim,updated_at FROM racing_rules ORDER BY sort_order,id`)
	if err != nil {
		return nil, err
	}
	rules := make(map[int]int)
	for rows.Next() {
		var rule RacingRule
		var kinds, filters string
		if err := rows.Scan(&rule.ID, &rule.Name, &rule.Enabled, &rule.SortOrder, &kinds, &filters, &rule.ReceiveWindowSeconds, &rule.TargetGroupID, &rule.TargetInstanceID, &rule.AllowOfficialReclaim, &rule.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(kinds), &rule.AcceptKinds); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(filters), &rule.Filters); err != nil {
			rows.Close()
			return nil, err
		}
		rule.SourceIDs = []int{}
		rules[rule.ID] = len(result.Rules)
		result.Rules = append(result.Rules, rule)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT rule_id,source_id FROM racing_rule_sources ORDER BY rule_id,source_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rule, source int
		if err := rows.Scan(&rule, &source); err != nil {
			rows.Close()
			return nil, err
		}
		index := rules[rule]
		result.Rules[index].SourceIDs = append(result.Rules[index].SourceIDs, source)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := readRacingStorage(ctx, tx, result); err != nil {
		return nil, err
	}
	result.ExecutionPolicies, err = readInstancePolicies(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
