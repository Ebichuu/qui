// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
)

func reclaimScope(scope string) (column, table string, err error) {
	switch scope {
	case "instances":
		return "instance_id", "instances", nil
	case "groups":
		return "group_id", "racing_groups", nil
	default:
		return "", "", racingInvalid("reclaim scope")
	}
}

func (s *RacingStore) SaveReclaimSetting(ctx context.Context, scope string, targetID int, policy RacingReclaimPolicy) error {
	column, table, err := reclaimScope(scope)
	if err != nil {
		return err
	}
	policy.RuleIDs, err = racingIDs(policy.RuleIDs)
	if err != nil {
		return err
	}
	if policy.MaxDeletes < 0 || policy.MaxReclaimBytes < 0 || policy.MaxRecentUploadBytes < 0 || policy.RecentUploadWindowSeconds < 0 || int64(policy.RecentUploadWindowSeconds) > int64((1<<63-1)/time.Second) || policy.MaxOvershootBytes < 0 || policy.MaxOvershootBytes > policy.MaxReclaimBytes {
		return racingInvalid("reclaim budgets")
	}
	if policy.Enabled && (len(policy.RuleIDs) == 0 || policy.MaxDeletes == 0 || policy.MaxReclaimBytes == 0 || policy.RecentUploadWindowSeconds == 0) {
		return racingInvalid("enabled reclaim requires rules and explicit budgets")
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	_, err = s.configurationWrite(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := racingExists(ctx, tx, table, targetID); err != nil {
			return 0, err
		}
		for _, id := range policy.RuleIDs {
			if err := racingExists(ctx, tx, "automations", id); err != nil {
				return 0, err
			}
		}
		var id int
		if err := tx.QueryRowContext(ctx, `INSERT INTO racing_reclaim_settings (`+column+`,policy) VALUES (?,?) ON CONFLICT (`+column+`) DO UPDATE SET policy=excluded.policy RETURNING id`, targetID, string(encoded)).Scan(&id); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM racing_reclaim_rule_refs WHERE setting_id=?`, id); err != nil {
			return 0, err
		}
		for _, ruleID := range policy.RuleIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO racing_reclaim_rule_refs(setting_id,rule_id) VALUES (?,?)`, id, ruleID); err != nil {
				return 0, err
			}
		}
		return id, nil
	})
	return err
}

// Removing the explicit value restores inheritance; saving Enabled=false is an
// explicit local override that suppresses every group default.
func (s *RacingStore) DeleteReclaimSetting(ctx context.Context, scope string, targetID int) error {
	column, table, err := reclaimScope(scope)
	if err != nil {
		return err
	}
	_, err = s.configurationWrite(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := racingExists(ctx, tx, table, targetID); err != nil {
			return 0, err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM racing_reclaim_settings WHERE `+column+`=?`, targetID)
		return 0, err
	})
	return err
}

// ReclaimConfiguration resolves all memberships in one read transaction. The
// result expresses configuration only, never candidate qualification/ownership.
func (s *RacingStore) ReclaimConfiguration(ctx context.Context) (*RacingReclaimConfiguration, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result := &RacingReclaimConfiguration{Settings: []RacingReclaimSetting{}, Effective: []RacingEffectiveReclaim{}}
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM racing_configuration_revision WHERE id=1`).Scan(&result.Revision); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT instance_id,group_id,policy FROM racing_reclaim_settings ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var instanceID, groupID *int
		var encoded string
		if err := rows.Scan(&instanceID, &groupID, &encoded); err != nil {
			rows.Close()
			return nil, err
		}
		item := RacingReclaimSetting{}
		if instanceID != nil {
			item.Scope = "instances"
			item.TargetID = *instanceID
		} else {
			item.Scope = "groups"
			item.TargetID = *groupID
		}
		if err := json.Unmarshal([]byte(encoded), &item.Policy); err != nil {
			rows.Close()
			return nil, err
		}
		result.Settings = append(result.Settings, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	groups := map[int][]int{}
	rows, err = tx.QueryContext(ctx, `SELECT m.instance_id,m.group_id FROM racing_group_members m JOIN racing_groups g ON g.id=m.group_id WHERE g.enabled=1 ORDER BY m.group_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var instanceID, groupID int
		if err := rows.Scan(&instanceID, &groupID); err != nil {
			rows.Close()
			return nil, err
		}
		groups[instanceID] = append(groups[instanceID], groupID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT id FROM instances ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		result.Effective = append(result.Effective, resolveReclaimSetting(id, groups[id], result.Settings))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func resolveReclaimSetting(instanceID int, groups []int, settings []RacingReclaimSetting) RacingEffectiveReclaim {
	result := RacingEffectiveReclaim{InstanceID: instanceID, State: "unconfigured", GroupIDs: []int{}}
	for _, setting := range settings {
		if setting.Scope == "instances" && setting.TargetID == instanceID {
			result.Policy = &setting.Policy
			result.State = "explicit"
			if !setting.Policy.Enabled {
				result.State = "disabled"
			}
			return result
		}
	}
	var selected *RacingReclaimPolicy
	conflict := false
	for _, setting := range settings {
		if setting.Scope != "groups" || !slices.Contains(groups, setting.TargetID) {
			continue
		}
		result.GroupIDs = append(result.GroupIDs, setting.TargetID)
		if selected == nil {
			selected = &setting.Policy
		} else if !equalReclaimPolicies(*selected, setting.Policy) {
			conflict = true
		}
	}
	slices.Sort(result.GroupIDs)
	if conflict {
		result.State = "conflict"
		return result
	}
	if selected != nil {
		result.Policy = selected
		result.State = "inherited"
		if !selected.Enabled {
			result.State = "disabled"
		}
	}
	return result
}

func equalReclaimPolicies(a, b RacingReclaimPolicy) bool {
	return a.Enabled == b.Enabled && slices.Equal(a.RuleIDs, b.RuleIDs) && a.MaxDeletes == b.MaxDeletes && a.MaxReclaimBytes == b.MaxReclaimBytes && a.MaxRecentUploadBytes == b.MaxRecentUploadBytes && a.RecentUploadWindowSeconds == b.RecentUploadWindowSeconds && a.MaxOvershootBytes == b.MaxOvershootBytes
}
