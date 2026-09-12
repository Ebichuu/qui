// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestReclaimPlanFrozenBudgetsAndAtomicClaims(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			ctx := t.Context()
			var rule int
			require.NoError(t, f.db.QueryRowContext(ctx, `INSERT INTO automations(instance_id,name,tracker_pattern,conditions) VALUES(?,?,?,?) RETURNING id`, f.instance, "Synthetic reclaim", "*", `{}`).Scan(&rule))
			policy := models.RacingReclaimPolicy{Enabled: true, RuleIDs: []int{rule}, MaxDeletes: 2, MaxReclaimBytes: 100, MaxRecentUploadBytes: 10, RecentUploadWindowSeconds: 60, MaxOvershootBytes: 20}
			require.NoError(t, f.store.SaveReclaimSetting(ctx, "instances", f.instance, policy))
			reservation := f.reservation(t, "official:event", "a", 90, f.pools[0])
			plan := models.RacingReclaimPlan{CandidateKey: reservation.Plan.CandidateKey, CandidateUpdatedAt: reservation.Plan.CandidateUpdatedAt, InstanceID: f.instance, PoolID: f.pools[0], ConfigurationRevision: reservation.Plan.ConfigurationRevision, Deadline: time.Now().Add(time.Minute), ObservedAt: time.Now(), DeficitBytes: 80, Items: []models.RacingReclaimItem{{Hash: "abc", AddedOn: 100, CapacityBytes: 90, RecentUploadBytes: 4}}}
			require.NoError(t, f.store.SaveReclaimPlan(ctx, plan))
			require.Error(t, f.store.BeginAutomaticDelete(ctx, f.instance, "bypass", "official-reclaim", models.DeleteModeWithFiles, []models.DeleteIdentity{{Hash: "abc", AddedOn: 100}}))
			original, err := f.store.ReclaimPlan(ctx, plan.CandidateKey)
			require.NoError(t, err)
			policy.MaxReclaimBytes = 200
			require.NoError(t, f.store.SaveReclaimSetting(ctx, "instances", f.instance, policy))
			require.ErrorIs(t, f.store.BeginReclaimDelete(ctx, plan.CandidateKey, "stale"), models.ErrRacingStale)
			config, err := f.store.ReclaimConfiguration(ctx)
			require.NoError(t, err)
			plan.ConfigurationRevision = config.Revision
			plan.Items[0].CapacityBytes = 110
			plan.DeficitBytes = 100
			require.ErrorIs(t, f.store.SaveReclaimPlan(ctx, plan), models.ErrRacingCapacity)
			plan.Items[0].CapacityBytes = 90
			plan.DeficitBytes = 80
			plan.Deadline = plan.Deadline.Add(time.Hour)
			require.NoError(t, f.store.SaveReclaimPlan(ctx, plan))
			persisted, err := f.store.ReclaimPlan(ctx, plan.CandidateKey)
			require.NoError(t, err)
			require.EqualValues(t, 100, persisted.Frozen.MaxReclaimBytes)
			require.True(t, original.Deadline.Equal(persisted.Deadline))
			for _, test := range []struct {
				name   string
				change func(*models.RacingReclaimPlan)
			}{
				{"stale observation", func(p *models.RacingReclaimPlan) { p.ObservedAt = time.Now().Add(-time.Minute) }},
				{"expired event", func(p *models.RacingReclaimPlan) { p.Deadline = time.Now().Add(-time.Second) }},
				{"insufficient combination", func(p *models.RacingReclaimPlan) { p.DeficitBytes = 99 }},
				{"target changed", func(p *models.RacingReclaimPlan) { p.PoolID = f.pools[1] }},
				{"candidate changed", func(p *models.RacingReclaimPlan) { p.CandidateUpdatedAt = "stale" }},
			} {
				t.Run(test.name, func(t *testing.T) {
					changed := plan
					test.change(&changed)
					require.Error(t, f.store.SaveReclaimPlan(ctx, changed))
				})
			}

			reopened, err := models.NewRacingStore(f.db, bytes.Repeat([]byte{9}, 32))
			require.NoError(t, err)
			var wg sync.WaitGroup
			results := make(chan error, 2)
			wg.Go(func() { results <- reopened.BeginReclaimDelete(ctx, plan.CandidateKey, "official-step") })
			wg.Go(func() {
				results <- f.store.BeginAutomaticDelete(ctx, f.instance, "daily-step", "daily", models.DeleteModeWithFiles, []models.DeleteIdentity{{Hash: "ABC", AddedOn: 100}})
			})
			wg.Wait()
			close(results)
			success := 0
			for err := range results {
				if err == nil {
					success++
				}
			}
			require.Equal(t, 1, success)
			persisted, err = reopened.ReclaimPlan(ctx, plan.CandidateKey)
			require.NoError(t, err)
			if persisted.Spent.Deletes == 0 {
				// If daily won, the failed reclaim claim must not consume any budget.
				require.Equal(t, models.RacingReclaimSpent{}, persisted.Spent)
				_, err = f.db.ExecContext(ctx, `DELETE FROM automatic_delete_intents WHERE operation_id='daily-step'`)
				require.NoError(t, err)
				require.NoError(t, reopened.BeginReclaimDelete(ctx, plan.CandidateKey, "official-step"))
			}
			require.NoError(t, reopened.RecordAutomaticDeleteResult(ctx, "official-step", false))
			persisted, err = reopened.ReclaimPlan(ctx, plan.CandidateKey)
			require.NoError(t, err)
			require.Equal(t, "awaiting_release", persisted.State)
			require.Equal(t, models.RacingReclaimSpent{Deletes: 1, CapacityBytes: 90, RecentUploadBytes: 4, OvershootBytes: 10}, persisted.Spent)
			require.Error(t, reopened.BeginReclaimDelete(ctx, plan.CandidateKey, "repeat"))
			require.Error(t, reopened.SaveReclaimPlan(ctx, plan))
			// Even task absence cannot clear the charge or authorize another step.
			pending, err := reopened.PendingAutomaticDeletes(ctx, f.instance)
			require.NoError(t, err)
			require.Len(t, pending, 1)
			require.NoError(t, reopened.ConfirmAutomaticDelete(ctx, pending[0], time.Now().Add(time.Second)))
			after, err := reopened.ReclaimPlan(ctx, plan.CandidateKey)
			require.NoError(t, err)
			require.Equal(t, persisted.Spent, after.Spent)
			require.Equal(t, "awaiting_release", after.State)
			owned, err := reopened.AutomaticDeleteOwned(ctx, f.instance, models.DeleteIdentity{Hash: "ABC", AddedOn: 100})
			require.NoError(t, err)
			require.True(t, owned)
			owned, err = reopened.AutomaticDeleteOwned(ctx, f.instance, models.DeleteIdentity{Hash: "ABC", AddedOn: 200})
			require.NoError(t, err)
			require.False(t, owned)
		})
	}
}
