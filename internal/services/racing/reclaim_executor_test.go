// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/qbittorrent"
	"github.com/autobrr/qui/internal/services/automations"
	"github.com/autobrr/qui/internal/services/racing/sources"
	"github.com/autobrr/qui/internal/testutil/testdb"
	"github.com/autobrr/qui/pkg/fileallocation"
)

type reclaimExecutionFake struct {
	*executionFake
	store               *models.RacingStore
	revision            int64
	accepted, recovered bool
	sends, checks       int
}

func (f *reclaimExecutionFake) ObserveReclaimCandidates(context.Context, int) (*automations.ReclaimCandidates, error) {
	zero := int64(0)
	return &automations.ReclaimCandidates{Revision: f.revision, Candidates: []automations.ReclaimCandidate{{Hash: "old", AddedOn: 100, SavePath: "/data", RecentUploadBytes: &zero, UploadWindowSeconds: 60, ObservedAt: time.Now()}}}, nil
}
func (f *reclaimExecutionFake) ReclaimPhysicalBytes(context.Context, int, automations.ReclaimCandidate, []int) (*int64, error) {
	n := int64(90)
	return &n, nil
}
func (f *reclaimExecutionFake) ReclaimReleaseBaseline(context.Context, int, automations.ReclaimCandidate, []int, string) (*fileallocation.ReleaseBaseline, error) {
	return &fileallocation.ReleaseBaseline{Root: "/data", Files: []string{"synthetic.bin"}, ExpectedBytes: 90, Space: fileallocation.Space{Device: 1, RootID: 2, Available: 1000, ObservedAt: time.Now()}}, nil
}
func (f *reclaimExecutionFake) ReclaimReleaseSpace(_ context.Context, _ int, b fileallocation.ReleaseBaseline) (fileallocation.Space, bool, error) {
	f.checks++
	return fileallocation.Space{Device: b.Space.Device, RootID: b.Space.RootID, Available: b.Space.Available + b.ExpectedBytes, ObservedAt: time.Now()}, f.recovered, nil
}
func (f *reclaimExecutionFake) ReclaimDelete(ctx context.Context, p models.RacingReclaimPlan, b fileallocation.ReleaseBaseline, commitments, _ string) error {
	if err := f.store.BeginReclaimDelete(ctx, p.CandidateKey, "synthetic-operation", b, models.DeleteIdentity{Hash: p.Items[0].Hash, AddedOn: p.Items[0].AddedOn}, p.InstanceID, commitments); err != nil {
		return err
	}
	f.sends++
	return f.store.RecordAutomaticDeleteResult(ctx, "synthetic-operation", f.accepted)
}
func (f *reclaimExecutionFake) ReconcileAutomaticDeletes(ctx context.Context, id int) error {
	pending, err := f.store.PendingAutomaticDeletes(ctx, id)
	if err != nil {
		return err
	}
	for _, p := range pending {
		if p.State == "accepted" {
			if err := f.store.ConfirmAutomaticDelete(ctx, p, time.Now()); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestReclaimExecutorPermissionAndRestart(t *testing.T) {
	for _, accepted := range []bool{true, false} {
		t.Run(map[bool]string{true: "accepted", false: "unknown"}[accepted], func(t *testing.T) {
			ctx := t.Context()
			db := testdb.NewMigratedSQLite(t, "reclaim-runtime")
			key := bytes.Repeat([]byte{7}, 32)
			store, err := models.NewRacingStore(db, key)
			require.NoError(t, err)
			instances, err := models.NewInstanceStore(db, key)
			require.NoError(t, err)
			instance, err := instances.Create(ctx, "Synthetic reclaim runtime", "http://127.0.0.1:1", "synthetic", "synthetic", nil, nil, false, nil)
			require.NoError(t, err)
			site, err := store.SaveSite(ctx, 0, models.RacingSiteInput{Name: "Synthetic site", BaseURL: "https://tracker.invalid", TrackerHosts: []string{"tracker.invalid"}, Enabled: true, RequestIntervalSeconds: 1})
			require.NoError(t, err)
			pool, err := store.SaveStoragePool(ctx, 0, models.RacingStoragePoolInput{Name: "Synthetic pool"})
			require.NoError(t, err)
			_, err = store.SavePathMapping(ctx, 0, models.RacingPathMappingInput{InstanceID: instance.ID, StoragePoolID: pool, Path: "/data"})
			require.NoError(t, err)
			policy := models.RacingInstancePolicy{InstanceID: instance.ID, Enabled: true, MaxConcurrentAdds: 1, MaxActiveDownloads: 4, SavePath: "/data"}
			require.NoError(t, store.SaveInstancePolicy(ctx, policy))
			var ruleID int
			require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO automations(instance_id,name,tracker_pattern,conditions) VALUES(?,?,?,?) RETURNING id`, instance.ID, "Synthetic low efficiency", "*", `{}`).Scan(&ruleID))
			require.NoError(t, store.SaveReclaimSetting(ctx, "instances", instance.ID, models.RacingReclaimPolicy{Enabled: true, RuleIDs: []int{ruleID}, MaxDeletes: 2, MaxReclaimBytes: 200, MaxRecentUploadBytes: 10, RecentUploadWindowSeconds: 60, MaxOvershootBytes: 20}))
			record := models.RacingCandidateRecord{Key: "synthetic:event", SiteID: site, EventKey: "event", FirstSeenAt: time.Now().UTC().Format(time.RFC3339Nano), State: "ready", Candidate: json.RawMessage(`{}`), Selection: json.RawMessage(`{"state":"ready"}`)}
			require.NoError(t, store.SaveCandidate(ctx, record, nil))
			records, err := store.CandidateRecords(ctx, "", nil, 100)
			require.NoError(t, err)
			record = records[0]
			now := time.Now()
			prefs := now.Add(-time.Second)
			free := int64(20)
			zero := int64(0)
			fake := &reclaimExecutionFake{executionFake: &executionFake{adds: map[int]int{}, observations: []qbittorrent.ExecutionObservation{{InstanceID: instance.ID, ObservedAt: &now, PreferencesObservedAt: &prefs, Fresh: true, Healthy: true, PreferencesFresh: true, SavePath: "/data", DefaultPathFreeBytes: &free, DownloadSpeed: &zero, UploadSpeed: &zero}}}, store: store, accepted: accepted}
			service := NewService(store)
			service.SetExecutionClients(fake, fake)
			service.SetReclaimCandidateReader(fake)
			config, err := store.Configuration(ctx)
			require.NoError(t, err)
			fake.revision = config.Revision
			plan := models.RacingReclaimPlan{CandidateKey: record.Key, CandidateUpdatedAt: record.UpdatedAt, InstanceID: instance.ID, PoolID: pool, ConfigurationRevision: config.Revision, Deadline: time.Now().Add(time.Minute), ObservedAt: time.Now(), DeficitBytes: 80, Items: []models.RacingReclaimItem{{Hash: "old", AddedOn: 100, CapacityBytes: 90}}}
			require.NoError(t, store.SaveReclaimPlan(ctx, plan))
			persisted, err := store.ReclaimPlan(ctx, record.Key)
			require.NoError(t, err)
			candidate := Candidate{VerifiedMetadata: &sources.VerifiedMetadata{HashV1: "new", SizeBytes: 100}}
			selection := RuleSelection{Rule: &models.RacingRule{Enabled: true, TargetInstanceID: &instance.ID}}
			service.executor.executeReclaim(ctx, *persisted, candidate, selection, config, fake, fake, []int{instance.ID})
			require.Zero(t, fake.sends, "reception alone cannot delete")
			policy.ReclaimEnabled = true
			require.NoError(t, store.SaveInstancePolicy(ctx, policy))
			config, err = store.Configuration(ctx)
			require.NoError(t, err)
			fake.revision = config.Revision
			plan.ConfigurationRevision = config.Revision
			plan.ObservedAt = time.Now()
			require.NoError(t, store.SaveReclaimPlan(ctx, plan))
			persisted, err = store.ReclaimPlan(ctx, record.Key)
			require.NoError(t, err)
			free = 100
			service.executor.executeReclaim(ctx, *persisted, candidate, selection, config, fake, fake, []int{instance.ID})
			require.Zero(t, fake.sends, "recovered capacity cancels deletion")
			free = 20
			service.executor.executeReclaim(ctx, *persisted, candidate, selection, config, fake, fake, []int{instance.ID})
			require.Equal(t, 1, fake.sends)
			service.executor.executeReclaim(ctx, *persisted, candidate, selection, config, fake, fake, []int{instance.ID})
			require.Equal(t, 1, fake.sends, "pending operation cannot resend")
			restart := newExecutionRunner(store, service)
			restart.launchReclaimReconciliation(ctx)
			restart.workers.Wait()
			pending, err := store.ReclaimPlan(ctx, record.Key)
			require.NoError(t, err)
			require.Equal(t, "awaiting_release", pending.State)
			policy.ReclaimEnabled = false
			require.NoError(t, store.SaveInstancePolicy(ctx, policy))
			fake.recovered = true
			// Cycle the page cursor without restarting again: pending evidence
			// must be revisited after reaching the end of the current page.
			restart.reclaimReleaseAttempt = time.Time{}
			restart.launchReclaimReconciliation(ctx)
			restart.workers.Wait()
			restart.reclaimReleaseAttempt = time.Time{}
			restart.launchReclaimReconciliation(ctx)
			restart.workers.Wait()
			final, err := store.ReclaimPlan(ctx, record.Key)
			require.NoError(t, err)
			require.Equal(t, pending.Spent, final.Spent)
			if accepted {
				require.Equal(t, "awaiting_executor", final.State)
				require.Empty(t, final.Items)
			} else {
				require.Equal(t, "awaiting_release", final.State)
				require.Zero(t, fake.checks)
			}
		})
	}
}
