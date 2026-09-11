// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/qbittorrent"
	"github.com/autobrr/qui/internal/services/racing/sources"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

type executionFake struct {
	mu           sync.Mutex
	observations []qbittorrent.ExecutionObservation
	adds         map[int]int
	slowID       int
	trackers     []qbt.TorrentTracker
}

func (f *executionFake) CachedExecutionObservations() []qbittorrent.ExecutionObservation {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := append([]qbittorrent.ExecutionObservation(nil), f.observations...)
	for i := range result {
		result[i].Torrents = append([]qbittorrent.ExecutionTorrent(nil), result[i].Torrents...)
	}
	return result
}
func (f *executionFake) AddTorrent(ctx context.Context, id int, _ []byte, _ map[string]string) (*qbt.TorrentAddResponse, error) {
	f.mu.Lock()
	f.adds[id]++
	slow := f.slowID == id
	f.mu.Unlock()
	if slow {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &qbt.TorrentAddResponse{SuccessCount: 1}, nil
}
func (f *executionFake) GetTorrentTrackers(context.Context, int, string) ([]qbt.TorrentTracker, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]qbt.TorrentTracker(nil), f.trackers...), nil
}
func (f *executionFake) addCount(id int) int { f.mu.Lock(); defer f.mu.Unlock(); return f.adds[id] }

func setupExecution(t *testing.T) (*Service, *models.RacingStore, *executionFake, []models.RacingAddIntent) {
	t.Helper()
	db := testdb.NewMigratedSQLite(t, "execution-service")
	key := bytes.Repeat([]byte{3}, 32)
	store, err := models.NewRacingStore(db, key)
	require.NoError(t, err)
	instanceStore, err := models.NewInstanceStore(db, key)
	require.NoError(t, err)
	site, err := store.SaveSite(t.Context(), 0, models.RacingSiteInput{Name: "Synthetic site", BaseURL: "https://tracker.invalid", TrackerHosts: []string{"tracker.invalid"}, Enabled: true, RequestIntervalSeconds: 1})
	require.NoError(t, err)
	endpoint := "https://tracker.invalid/feed"
	source, err := store.SaveSource(t.Context(), 0, models.RacingSourceInput{SiteID: site, Name: "Synthetic source", Enabled: true, Kind: "rss", Adapter: "chd", IntervalSeconds: 10, URL: &endpoint})
	require.NoError(t, err)
	fake := &executionFake{adds: map[int]int{}, trackers: []qbt.TorrentTracker{{Url: "https://tracker.invalid/announce?key=synthetic"}}}
	ids, pools := make([]int, 0, 2), make([]int, 0, 2)
	for i := range 2 {
		instance, err := instanceStore.Create(t.Context(), fmt.Sprintf("Synthetic %d", i), "http://127.0.0.1:1", "test", "test", nil, nil, false, nil)
		require.NoError(t, err)
		ids = append(ids, instance.ID)
		require.NoError(t, store.SaveInstancePolicy(t.Context(), models.RacingInstancePolicy{InstanceID: instance.ID, Enabled: true, MaxConcurrentAdds: 1, MaxActiveDownloads: 3, SavePath: "/data"}))
		pool, err := store.SaveStoragePool(t.Context(), 0, models.RacingStoragePoolInput{Name: fmt.Sprintf("Disk %d", i)})
		require.NoError(t, err)
		pools = append(pools, pool)
		_, err = store.SavePathMapping(t.Context(), 0, models.RacingPathMappingInput{InstanceID: instance.ID, StoragePoolID: pool, Path: "/data"})
		require.NoError(t, err)
		_, err = store.SaveRule(t.Context(), 0, models.RacingRuleInput{Name: fmt.Sprintf("Rule %d", i), Enabled: true, SourceIDs: []int{source}, AcceptKinds: []string{"official"}, ReceiveWindowSeconds: 900, TargetInstanceID: &instance.ID})
		require.NoError(t, err)
		now := time.Now()
		prefs := now.Add(-time.Second)
		free, zero := int64(10000), int64(0)
		fake.observations = append(fake.observations, qbittorrent.ExecutionObservation{InstanceID: instance.ID, ObservedAt: &now, PreferencesObservedAt: &prefs, Fresh: true, Healthy: true, PreferencesFresh: true, SavePath: "/data", DefaultPathFreeBytes: &free, DownloadSpeed: &zero, UploadSpeed: &zero})
	}
	config, err := store.Configuration(t.Context())
	require.NoError(t, err)
	service := NewService(store)
	service.configuration = config
	service.status.ConfigurationReady = true
	service.SetExecutionClients(fake, fake)
	announce := "https://tracker.invalid/announce?key=synthetic"
	raw := []byte(fmt.Sprintf("d8:announce%d:%s4:infod6:lengthi1024e4:name14:Example Aurora12:piece lengthi16384e6:pieces20:abcdefghijklmnopqrstee", len(announce), announce))
	intents := make([]models.RacingAddIntent, 0, len(ids))
	for i, id := range ids {
		payload := bytes.ReplaceAll(raw, []byte("Example Aurora"), []byte(fmt.Sprintf("Example Auror%c", 'a'+i)))
		proof, err := sources.ParseMetainfo(payload)
		require.NoError(t, err)
		key := fmt.Sprintf("event:%d", i)
		now := time.Now().UTC()
		record := models.RacingCandidateRecord{Key: key, SiteID: site, EventKey: key, State: "ready", FirstSeenAt: now.Format(time.RFC3339Nano), Candidate: json.RawMessage(`{}`), Selection: json.RawMessage(`{"state":"ready"}`)}
		require.NoError(t, store.SaveCandidate(t.Context(), record, nil))
		records, err := store.CandidateRecords(t.Context(), "", nil, 100)
		require.NoError(t, err)
		for _, item := range records {
			if item.Key == key {
				record = item
			}
		}
		target, ok := makeReceptionTarget(config, fake.observations[i], config.ExecutionPolicies[i])
		require.True(t, ok)
		plan := models.RacingAddPlan{CandidateKey: key, SiteID: site, InstanceID: id, Rule: config.Rules[i], Policy: config.ExecutionPolicies[i], TrackerHosts: []string{"tracker.invalid"}, HashV1: proof.HashV1, SizeBytes: proof.SizeBytes, PoolIDs: []int{pools[i]}, Options: target.options, Deadline: now.Add(time.Minute).Format(time.RFC3339Nano), FirstSeenAt: record.FirstSeenAt, CandidateUpdatedAt: record.UpdatedAt, ConfigurationRevision: config.Revision}
		budgets := executionBudgets(config, fake.observations, nil, time.Now())
		require.NoError(t, store.ReserveAdd(t.Context(), models.RacingReservation{Plan: plan, Metainfo: payload, Pools: budgets, ObservedAt: *fake.observations[i].ObservedAt}))
		intent, err := store.AddIntent(t.Context(), key)
		require.NoError(t, err)
		intents = append(intents, intent)
	}
	return service, store, fake, intents
}

func TestExecutionSlowInstanceAndRestartNeverResend(t *testing.T) {
	service, store, fake, intents := setupExecution(t)
	fake.slowID = intents[0].InstanceID
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	service.executor.launch(ctx, intents[0], fake, fake)
	service.executor.launch(ctx, intents[1], fake, fake)
	require.Eventually(t, func() bool {
		return fake.addCount(intents[0].InstanceID) == 1 && fake.addCount(intents[1].InstanceID) == 1
	}, 3*time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		item, err := store.AddIntent(t.Context(), intents[1].CandidateKey)
		return err == nil && item.State == "accepted"
	}, time.Second, time.Millisecond)
	cancel()
	service.executor.workers.Wait()
	slow, err := store.AddIntent(t.Context(), intents[0].CandidateKey)
	require.NoError(t, err)
	require.Contains(t, []string{"submitted", "unknown"}, slow.State)
	restarted := newExecutionRunner(store, service)
	restarted.execute(t.Context(), slow, fake, fake)
	require.Equal(t, 1, fake.addCount(slow.InstanceID), "a missing task after timeout is observed, never sent again")
	// The original task eventually appears paused; identity confirmation does not
	// claim it was runnable or had transferred data.
	fake.mu.Lock()
	now := time.Now()
	fake.observations[0].ObservedAt = &now
	fake.observations[0].Torrents = []qbittorrent.ExecutionTorrent{{Hash: slow.Plan.HashV1, SavePath: "/data", Size: 1024, Remaining: 1024, State: qbt.TorrentStatePausedDl}}
	fake.mu.Unlock()
	restarted.execute(t.Context(), slow, fake, fake)
	slow, err = store.AddIntent(t.Context(), slow.CandidateKey)
	require.NoError(t, err)
	require.Equal(t, "confirmed", slow.State)
	require.Nil(t, slow.RunnableAt)
	require.Nil(t, slow.TransferredAt)
	fake.mu.Lock()
	now = time.Now()
	fake.observations[0].ObservedAt = &now
	fake.observations[0].Torrents[0].State = qbt.TorrentStateDownloading
	fake.observations[0].Torrents[0].Downloaded = 1
	fake.mu.Unlock()
	restarted.execute(t.Context(), slow, fake, fake)
	slow, err = store.AddIntent(t.Context(), slow.CandidateKey)
	require.NoError(t, err)
	require.NotNil(t, slow.RunnableAt)
	require.NotNil(t, slow.TransferredAt)
}

func TestExecutionExistingHashDoesNotMergeAnotherAccountTracker(t *testing.T) {
	service, store, fake, intents := setupExecution(t)
	intent := intents[0]
	fake.mu.Lock()
	fake.observations[0].Torrents = []qbittorrent.ExecutionTorrent{{Hash: intent.Plan.HashV1, SavePath: "/data", Size: 1024, Remaining: 1024, State: qbt.TorrentStateDownloading}}
	fake.trackers = []qbt.TorrentTracker{{Url: "https://tracker.invalid/announce?key=different-account"}}
	fake.mu.Unlock()
	service.executor.execute(t.Context(), intent, fake, fake)
	require.Zero(t, fake.addCount(intent.InstanceID))
	current, err := store.AddIntent(t.Context(), intent.CandidateKey)
	require.NoError(t, err)
	require.Equal(t, "reserved", current.State)
	require.Equal(t, "existing_tracker_mismatch", current.Reason)
	fake.mu.Lock()
	fake.trackers = []qbt.TorrentTracker{{Url: "https://tracker.invalid/announce?key=synthetic"}}
	fake.mu.Unlock()
	service.executor.execute(t.Context(), current, fake, fake)
	current, err = store.AddIntent(t.Context(), intent.CandidateKey)
	require.NoError(t, err)
	require.Equal(t, "confirmed", current.State)
	require.Zero(t, fake.addCount(intent.InstanceID), "existing matching task is confirmed without another add request")
}

func TestExecutionChangedPolicyCancelsBeforeNetwork(t *testing.T) {
	service, store, fake, intents := setupExecution(t)
	intent := intents[0]
	policy := intent.Plan.Policy
	policy.Enabled = false
	require.NoError(t, store.SaveInstancePolicy(t.Context(), policy))
	service.executor.execute(t.Context(), intent, fake, fake)
	current, err := store.AddIntent(t.Context(), intent.CandidateKey)
	require.NoError(t, err)
	require.Equal(t, "cancelled", current.State)
	require.Zero(t, fake.addCount(intent.InstanceID))
}
