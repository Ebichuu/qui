// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"testing"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/qbittorrent"
)

func executionFixture() (*models.RacingConfiguration, []qbittorrent.ExecutionObservation) {
	now := time.Now().Add(-time.Millisecond)
	prefs := now.Add(-time.Second)
	config := &models.RacingConfiguration{StoragePools: []models.RacingStoragePool{{ID: 1}, {ID: 2}}, PathMappings: []models.RacingPathMapping{{InstanceID: 1, StoragePoolID: 1, Path: "/data"}, {InstanceID: 2, StoragePoolID: 1, Path: "/data"}, {InstanceID: 3, StoragePoolID: 2, Path: "/other"}}}
	var observations []qbittorrent.ExecutionObservation
	for i := 1; i <= 3; i++ {
		free, load := int64(100), int64(0)
		path := "/data"
		if i == 3 {
			path = "/other"
		}
		observations = append(observations, qbittorrent.ExecutionObservation{InstanceID: i, Healthy: true, Fresh: true, PreferencesFresh: true, ObservedAt: &now, PreferencesObservedAt: &prefs, SavePath: path, DefaultPathFreeBytes: &free, DownloadSpeed: &load, UploadSpeed: &load, Categories: map[string]qbt.Category{}})
	}
	return config, observations
}

func TestExecutionBudgetsSharedDisksAndPausedFutureWrites(t *testing.T) {
	config, observations := executionFixture()
	observations[0].Torrents = []qbittorrent.ExecutionTorrent{{Hash: "a", SavePath: "/data/jobs", Size: 70, Remaining: 40, State: qbt.TorrentStatePausedDl}}
	observations[1].Torrents = []qbittorrent.ExecutionTorrent{{Hash: "b", SavePath: "/data", Size: 30, Remaining: 20, State: qbt.TorrentStateQueuedDl}}
	budgets := executionBudgets(config, observations, nil, time.Now())
	require.Len(t, budgets, 2)
	require.Equal(t, int64(40), budgets[0].AvailableBytes, "do not sum duplicate free-space readings; paused and queued work still consumes future capacity")
	require.Equal(t, int64(100), budgets[1].AvailableBytes)
	observations[1].Fresh = false
	budgets = executionBudgets(config, observations, nil, time.Now())
	require.Len(t, budgets, 1)
	require.Equal(t, 2, budgets[0].StoragePoolID, "a stale member blocks its shared disk, not an unrelated disk")
}

func TestExecutionBudgetsTemporaryDiskPromisesWholeFinalCopy(t *testing.T) {
	config, observations := executionFixture()
	config.PathMappings = append(config.PathMappings, models.RacingPathMapping{InstanceID: 1, StoragePoolID: 2, Path: "/other"})
	observations[0].TempPathEnabled = true
	observations[0].TempPath = "/other/incomplete"
	observations[0].Torrents = []qbittorrent.ExecutionTorrent{{SavePath: "/data", DownloadPath: "/other/incomplete", Size: 70, Remaining: 10, State: qbt.TorrentStateDownloading}}
	budgets := executionBudgets(config, observations, nil, time.Now())
	require.Len(t, budgets, 2)
	require.Equal(t, int64(30), budgets[0].AvailableBytes, "final disk must hold the complete copy, including bytes downloaded elsewhere")
	require.Equal(t, int64(90), budgets[1].AvailableBytes, "temporary disk only needs remaining writes")
	observations[0].Torrents[0].DownloadPath = "/unknown"
	require.Empty(t, executionBudgets(config, observations, nil, time.Now()), "an unresolved future path cannot be treated as zero cost")
}

func TestReceptionTargetKeepsWinningGroupAndFiltersCapacity(t *testing.T) {
	config, observations := executionFixture()
	group := 10
	config.Groups = []models.RacingGroup{{ID: group, Enabled: true, InstanceIDs: []int{1, 2}}}
	policies := []models.RacingInstancePolicy{{InstanceID: 1, Enabled: true, MaxConcurrentAdds: 2, MaxActiveDownloads: 4, MinFreeBytes: 10}, {InstanceID: 2, Enabled: true, MaxConcurrentAdds: 2, MaxActiveDownloads: 4, MinFreeBytes: 10}, {InstanceID: 3, Enabled: true, MaxConcurrentAdds: 2, MaxActiveDownloads: 4}}
	rule := models.RacingRule{TargetGroupID: &group}
	budgets := executionBudgets(config, observations, nil, time.Now())
	targets := receptionTargets(rule, config, policies, observations, nil, budgets, 70, time.Now())
	require.Len(t, targets, 2)
	require.Equal(t, 1, targets[0].instance.InstanceID)
	load := int64(10)
	observations[0].DownloadSpeed = &load
	targets = receptionTargets(rule, config, policies, observations, nil, budgets, 70, time.Now())
	require.Equal(t, 2, targets[0].instance.InstanceID)
	intent := models.RacingAddIntent{CandidateKey: "a", InstanceID: 1, State: "unknown", Plan: models.RacingAddPlan{SizeBytes: 70, PoolIDs: []int{1}}}
	require.Empty(t, receptionTargets(rule, config, policies, observations, []models.RacingAddIntent{intent}, budgets, 40, time.Now()), "other groups cannot provide a fallback for the winning rule")
	policies[0].Enabled = false
	policies[1].Enabled = false
	require.Empty(t, receptionTargets(rule, config, policies, observations, nil, budgets, 10, time.Now()), "configuration alone never enables reception")
}

func TestReceptionTargetCategoryAndTemporaryMapping(t *testing.T) {
	config, observations := executionFixture()
	policy := models.RacingInstancePolicy{InstanceID: 1, Enabled: true, AutoTMM: true, Category: "films"}
	observations[0].Categories["films"] = qbt.Category{SavePath: "/unmapped"}
	_, ok := makeReceptionTarget(config, observations[0], policy)
	require.False(t, ok)
	observations[0].Categories["films"] = qbt.Category{SavePath: "/data/films"}
	target, ok := makeReceptionTarget(config, observations[0], policy)
	require.True(t, ok)
	require.NotContains(t, target.options, "savepath")
	require.Equal(t, "false", target.options["paused"])
	require.Equal(t, "false", target.options["stopped"])
	observations[0].TempPathEnabled = true
	observations[0].TempPath = "/other"
	_, ok = makeReceptionTarget(config, observations[0], policy)
	require.False(t, ok)
	config.PathMappings = append(config.PathMappings, models.RacingPathMapping{InstanceID: 1, StoragePoolID: 2, Path: "/other"})
	target, ok = makeReceptionTarget(config, observations[0], policy)
	require.True(t, ok)
	require.Equal(t, []int{1, 2}, target.pools)
}

func TestReceptionAvoidsAccidentalCrossInstanceParticipation(t *testing.T) {
	_, observations := executionFixture()
	targets := []receptionTarget{{instance: observations[0]}, {instance: observations[1]}, {instance: observations[2]}}
	observations[1].Torrents = []qbittorrent.ExecutionTorrent{{Hash: "verified-hash"}}
	filtered := avoidDuplicateParticipants(targets, observations, "verified-hash", "")
	require.Len(t, filtered, 1)
	require.Equal(t, 2, filtered[0].instance.InstanceID)
	require.Empty(t, avoidDuplicateParticipants([]receptionTarget{{instance: observations[0]}}, observations, "verified-hash", ""), "cannot borrow another target when the selected instance does not host existing content")
}

func TestReceptionHistoryBalancesNearLoadWithoutOverridingSafety(t *testing.T) {
	config, observations := executionFixture()
	group := 10
	config.Groups = []models.RacingGroup{{ID: group, Enabled: true, InstanceIDs: []int{1, 3}}}
	policies := []models.RacingInstancePolicy{{InstanceID: 1, Enabled: true, MaxConcurrentAdds: 2, MaxActiveDownloads: 4}, {InstanceID: 3, Enabled: true, MaxConcurrentAdds: 2, MaxActiveDownloads: 4}}
	rule := models.RacingRule{TargetGroupID: &group}
	slow, near, busy := int64(10<<20), int64(10<<20)+100, int64(100<<20)
	observations[0].DownloadSpeed = &slow
	observations[2].DownloadSpeed = &near
	budgets := executionBudgets(config, observations, nil, time.Now())
	history := make([]models.RacingAddIntent, 0, 20)
	counts := map[int]int{}
	for i := range 20 {
		targets := receptionTargets(rule, config, policies, observations, history, budgets, 10, time.Now())
		require.Len(t, targets, 2)
		id := targets[0].instance.InstanceID
		counts[id]++
		history = append(history, models.RacingAddIntent{InstanceID: id, State: "retired", ReservedAt: time.Now().Add(time.Duration(i) * time.Second).UTC().Format(time.RFC3339Nano)})
	}
	require.Equal(t, 10, counts[1])
	require.Equal(t, 10, counts[3], "completed history should still compensate small load differences")
	observations[2].UploadSpeed = &busy
	targets := receptionTargets(rule, config, policies, observations, history, budgets, 10, time.Now())
	require.Equal(t, 1, targets[0].instance.InstanceID, "busy completed uploads remain real pressure")
	observations[2].UploadSpeed = &slow
	budgets[1].AvailableBytes = 5
	targets = receptionTargets(rule, config, policies, observations, history, budgets, 10, time.Now())
	require.Len(t, targets, 1)
	require.Equal(t, 1, targets[0].instance.InstanceID, "history cannot override insufficient capacity")
	observations[0].Fresh = false
	require.Empty(t, receptionTargets(rule, config, policies, observations, history, budgets, 10, time.Now()))
}
