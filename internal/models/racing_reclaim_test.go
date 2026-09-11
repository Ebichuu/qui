// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestRacingReclaimInheritance(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			ctx := t.Context()
			var rule int
			require.NoError(t, f.db.QueryRowContext(ctx, `INSERT INTO automations(instance_id,name,tracker_pattern,conditions) VALUES(?,?,?,?) RETURNING id`, f.instance, "Synthetic candidate", "*", `{}`).Scan(&rule))
			group := models.RacingGroupInput{Name: "Synthetic group", Enabled: true, InstanceIDs: []int{f.instance}}
			first, err := f.store.SaveGroup(ctx, 0, group)
			require.NoError(t, err)
			group.Name = "Second group"
			second, err := f.store.SaveGroup(ctx, 0, group)
			require.NoError(t, err)
			read := func() models.RacingEffectiveReclaim {
				t.Helper()
				c, err := f.store.ReclaimConfiguration(ctx)
				require.NoError(t, err)
				require.Len(t, c.Effective, 1)
				return c.Effective[0]
			}
			require.Equal(t, "unconfigured", read().State)
			policy := models.RacingReclaimPolicy{Enabled: true, RuleIDs: []int{rule, rule}, MaxDeletes: 2, MaxReclaimBytes: 100, MaxRecentUploadBytes: 0, RecentUploadWindowSeconds: 3600, MaxOvershootBytes: 10}
			require.NoError(t, f.store.SaveReclaimSetting(ctx, "groups", first, policy))
			require.NoError(t, f.store.SaveReclaimSetting(ctx, "groups", second, policy))
			result := read()
			require.Equal(t, "inherited", result.State)
			require.Equal(t, []int{rule}, result.Policy.RuleIDs)
			require.Equal(t, []int{first, second}, result.GroupIDs)
			policy.MaxDeletes = 3
			require.NoError(t, f.store.SaveReclaimSetting(ctx, "groups", second, policy))
			result = read()
			require.Equal(t, "conflict", result.State)
			require.Nil(t, result.Policy)
			require.NoError(t, f.store.SaveReclaimSetting(ctx, "instances", f.instance, models.RacingReclaimPolicy{}))
			result = read()
			require.Equal(t, "disabled", result.State)
			require.Empty(t, result.GroupIDs)
			require.NoError(t, f.store.SaveReclaimSetting(ctx, "instances", f.instance, policy))
			require.Equal(t, "explicit", read().State)
			require.NoError(t, f.store.DeleteReclaimSetting(ctx, "instances", f.instance))
			require.Equal(t, "conflict", read().State)
			group.Enabled = false
			_, err = f.store.SaveGroup(ctx, second, group)
			require.NoError(t, err)
			require.Equal(t, "inherited", read().State)
			group.Enabled = true
			group.InstanceIDs = []int{}
			_, err = f.store.SaveGroup(ctx, second, group)
			require.NoError(t, err)
			require.Equal(t, []int{first}, read().GroupIDs)
			auto := models.NewAutomationStore(f.db)
			require.ErrorIs(t, auto.Delete(ctx, f.instance, rule), models.ErrRacingReferenced)
			_, err = f.db.ExecContext(ctx, `UPDATE automations SET name=? WHERE id=?`, "Renamed synthetic candidate", rule)
			require.NoError(t, err)
			require.Equal(t, []int{rule}, read().Policy.RuleIDs)
			before, err := f.store.ReclaimConfiguration(ctx)
			require.NoError(t, err)
			policy.RuleIDs = []int{999999}
			require.ErrorIs(t, f.store.SaveReclaimSetting(ctx, "groups", first, policy), models.ErrRacingInvalid)
			after, err := f.store.ReclaimConfiguration(ctx)
			require.NoError(t, err)
			require.Equal(t, before, after)
			policy.RuleIDs = []int{rule}
			policy.MaxReclaimBytes = -1
			require.ErrorIs(t, f.store.SaveReclaimSetting(ctx, "instances", f.instance, policy), models.ErrRacingInvalid)
			require.NoError(t, f.store.DeleteReclaimSetting(ctx, "groups", first))
			require.NoError(t, f.store.DeleteReclaimSetting(ctx, "groups", second))
			require.NoError(t, auto.Delete(ctx, f.instance, rule))
			require.Equal(t, "unconfigured", read().State)
		})
	}
}
