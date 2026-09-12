// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/pkg/fileallocation"
)

func TestReclaimReleaseRetainsChargesAndSerializesPool(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			ctx := t.Context()
			var rule int
			require.NoError(t, f.db.QueryRowContext(ctx, `INSERT INTO automations(instance_id,name,tracker_pattern,conditions) VALUES(?,?,?,?) RETURNING id`, f.instance, "Synthetic release", "*", `{}`).Scan(&rule))
			policy := models.RacingReclaimPolicy{Enabled: true, RuleIDs: []int{rule}, MaxDeletes: 3, MaxReclaimBytes: 300, MaxRecentUploadBytes: 30, RecentUploadWindowSeconds: 60, MaxOvershootBytes: 30}
			require.NoError(t, f.store.SaveReclaimSetting(ctx, "instances", f.instance, policy))
			makePlan := func(key, hash string) models.RacingReclaimPlan {
				reservation := f.reservation(t, key, hash, 90, f.pools[0])
				plan := models.RacingReclaimPlan{CandidateKey: key, CandidateUpdatedAt: reservation.Plan.CandidateUpdatedAt, InstanceID: f.instance, PoolID: f.pools[0], ConfigurationRevision: reservation.Plan.ConfigurationRevision, Deadline: time.Now().Add(time.Minute), ObservedAt: time.Now(), DeficitBytes: 80, Items: []models.RacingReclaimItem{{Hash: hash, AddedOn: 100, CapacityBytes: 90, RecentUploadBytes: 4}}}
				require.NoError(t, f.store.SaveReclaimPlan(ctx, plan))
				return plan
			}
			first, second := makePlan("event:first", "a"), makePlan("event:second", "b")
			baseline := fileallocation.ReleaseBaseline{Root: t.TempDir(), Files: []string{"synthetic.bin"}, ExpectedBytes: 90, Space: fileallocation.Space{Device: 1, RootID: 2, Available: 100, ObservedAt: time.Now()}}
			invalid := baseline
			invalid.ExpectedBytes = 1
			require.Error(t, f.store.BeginReclaimDelete(ctx, first.CandidateKey, "invalid", invalid))
			require.NoError(t, f.store.BeginReclaimDelete(ctx, first.CandidateKey, "first", baseline))
			reopened, err := models.NewRacingStore(f.db, bytes.Repeat([]byte{9}, 32))
			require.NoError(t, err)
			receipt, err := reopened.ReclaimRelease(ctx, "first")
			require.NoError(t, err)
			require.Equal(t, baseline.Root, receipt.Baseline.Root)
			require.Equal(t, baseline.ExpectedBytes, receipt.Baseline.ExpectedBytes)
			require.Equal(t, "pending", receipt.State)
			require.ErrorIs(t, reopened.BeginReclaimDelete(ctx, second.CandidateKey, "second", baseline), models.ErrRacingIntentState)
			unspent, err := reopened.ReclaimPlan(ctx, second.CandidateKey)
			require.NoError(t, err)
			require.Zero(t, unspent.Spent.Deletes)
			observed := fileallocation.Space{Device: 1, RootID: 2, Available: 190, ObservedAt: time.Now()}
			require.Error(t, reopened.RecordReclaimRelease(ctx, "first", observed), "submitted is not task absence")
			require.NoError(t, reopened.RecordAutomaticDeleteResult(ctx, "first", true))
			require.Error(t, reopened.RecordReclaimRelease(ctx, "first", observed), "accepted alone is insufficient")
			pending, err := reopened.PendingAutomaticDeletes(ctx, f.instance)
			require.NoError(t, err)
			require.Len(t, pending, 1)
			require.NoError(t, reopened.ConfirmAutomaticDelete(ctx, pending[0], time.Now()))
			observed.ObservedAt = time.Now()
			for _, mutate := range []func(*fileallocation.Space){
				func(s *fileallocation.Space) { s.Available = 189 },
				func(s *fileallocation.Space) { s.Device++ },
				func(s *fileallocation.Space) { s.RootID++ },
				func(s *fileallocation.Space) { s.ObservedAt = time.Now().Add(-time.Minute) },
				func(s *fileallocation.Space) { s.ObservedAt = time.Now().Add(time.Minute) },
			} {
				bad := observed
				mutate(&bad)
				require.Error(t, reopened.RecordReclaimRelease(ctx, "first", bad))
			}
			observed.ObservedAt = time.Now()
			require.NoError(t, reopened.RecordReclaimRelease(ctx, "first", observed))
			require.Error(t, reopened.RecordReclaimRelease(ctx, "first", observed), "a receipt cannot be reused")
			finished, err := reopened.ReclaimPlan(ctx, first.CandidateKey)
			require.NoError(t, err)
			require.Equal(t, "awaiting_executor", finished.State)
			require.Empty(t, finished.Items)
			require.True(t, finished.ObservedAt.IsZero())
			require.Equal(t, models.RacingReclaimSpent{Deletes: 1, CapacityBytes: 90, RecentUploadBytes: 4, OvershootBytes: 10}, finished.Spent)
			require.Error(t, reopened.BeginReclaimDelete(ctx, first.CandidateKey, "stale-items", baseline))
			// A new baseline is essential: the next operation must not count the
			// previous recovery again. These are synthetic store-level receipts.
			require.ErrorIs(t, reopened.BeginReclaimDelete(ctx, second.CandidateKey, "old-baseline", baseline), models.ErrRacingStale)
			baseline.Space = observed
			baseline.Space.ObservedAt = time.Now()
			require.NoError(t, reopened.BeginReclaimDelete(ctx, second.CandidateKey, "second", baseline))
			receipt, err = reopened.ReclaimRelease(ctx, "first")
			require.NoError(t, err)
			require.Equal(t, "observed", receipt.State)
			// Recreate the pre-migration layout in this isolated fixture. The
			// unresolved old operation must retain occupancy with unknown evidence.
			_, err = f.db.ExecContext(ctx, `DROP TABLE racing_reclaim_releases`)
			require.NoError(t, err)
			directory, migration := "migrations", "110_reclaim_releases.sql"
			if engine == "postgres" {
				directory, migration = "postgres_migrations", "111_reclaim_releases.sql"
			}
			sql, err := os.ReadFile(filepath.Join("..", "database", directory, migration))
			require.NoError(t, err)
			tx, err := f.db.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			_, err = tx.ExecContext(ctx, string(sql))
			require.NoError(t, err)
			require.NoError(t, tx.Commit())
			legacy, err := reopened.ReclaimRelease(ctx, "second")
			require.NoError(t, err)
			require.Equal(t, "pending", legacy.State)
			require.Zero(t, legacy.Baseline.ExpectedBytes)
			require.Error(t, reopened.RecordReclaimRelease(ctx, "second", observed))
			first.ObservedAt = time.Now()
			require.NoError(t, reopened.SaveReclaimPlan(ctx, first))
			require.ErrorIs(t, reopened.BeginReclaimDelete(ctx, first.CandidateKey, "blocked-by-legacy", baseline), models.ErrRacingIntentState)
		})
	}
}
