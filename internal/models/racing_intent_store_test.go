// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/database"
	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

type racingExecutionFixture struct {
	db       *database.DB
	store    *models.RacingStore
	instance int
	site     int
	pools    []int
	rule     models.RacingRule
}

func newRacingExecutionFixture(t *testing.T, engine string) racingExecutionFixture {
	t.Helper()
	f := racingExecutionFixture{}
	if engine == "sqlite" {
		f.db = testdb.NewMigratedSQLite(t, "racing-execution")
	} else {
		f.db = testdb.NewMigratedPostgres(t, "racing-execution")
	}
	key := bytes.Repeat([]byte{9}, 32)
	var err error
	f.store, err = models.NewRacingStore(f.db, key)
	require.NoError(t, err)
	instances, err := models.NewInstanceStore(f.db, key)
	require.NoError(t, err)
	instance, err := instances.Create(t.Context(), "Synthetic reception", "http://127.0.0.1:1", "test", "test", nil, nil, false, nil)
	require.NoError(t, err)
	f.instance = instance.ID
	require.NoError(t, f.store.SaveInstancePolicy(t.Context(), models.RacingInstancePolicy{InstanceID: f.instance, Enabled: true, MaxConcurrentAdds: 8, MaxActiveDownloads: 12, MinFreeBytes: 10}))
	f.site, err = f.store.SaveSite(t.Context(), 0, models.RacingSiteInput{Name: "Synthetic site", BaseURL: "https://tracker.invalid", Enabled: true, RequestIntervalSeconds: 1})
	require.NoError(t, err)
	endpoint := "https://tracker.invalid/feed"
	source, err := f.store.SaveSource(t.Context(), 0, models.RacingSourceInput{SiteID: f.site, Name: "Synthetic feed", Kind: "rss", Adapter: "chd", Enabled: true, IntervalSeconds: 10, URL: &endpoint})
	require.NoError(t, err)
	_, err = f.store.SaveRule(t.Context(), 0, models.RacingRuleInput{Name: "Official first", Enabled: true, SourceIDs: []int{source}, AcceptKinds: []string{"official"}, ReceiveWindowSeconds: 900, TargetInstanceID: &f.instance})
	require.NoError(t, err)
	for i := range 2 {
		id, err := f.store.SaveStoragePool(t.Context(), 0, models.RacingStoragePoolInput{Name: fmt.Sprintf("Disk %d", i)})
		require.NoError(t, err)
		f.pools = append(f.pools, id)
	}
	config, err := f.store.Configuration(t.Context())
	require.NoError(t, err)
	f.rule = config.Rules[0]
	return f
}

func (f racingExecutionFixture) reservation(t *testing.T, key string, hash string, size int64, pools ...int) models.RacingReservation {
	t.Helper()
	now := time.Now().UTC()
	record := models.RacingCandidateRecord{Key: key, SiteID: f.site, EventKey: key, FirstSeenAt: now.Format(time.RFC3339Nano), Candidate: json.RawMessage(`{}`), Selection: json.RawMessage(`{"state":"ready"}`), State: "ready"}
	require.NoError(t, f.store.SaveCandidate(t.Context(), record, nil))
	records, err := f.store.CandidateRecords(t.Context(), "", nil, 100)
	require.NoError(t, err)
	for _, current := range records {
		if current.Key == key {
			record = current
		}
	}
	config, err := f.store.Configuration(t.Context())
	require.NoError(t, err)
	policies, err := f.store.InstancePolicies(t.Context())
	require.NoError(t, err)
	result := models.RacingReservation{Plan: models.RacingAddPlan{CandidateKey: key, SiteID: f.site, InstanceID: f.instance, Rule: f.rule, Policy: policies[0], HashV1: strings.Repeat(hash, 40), SizeBytes: size, PoolIDs: pools, FirstSeenAt: record.FirstSeenAt, Deadline: now.Add(time.Minute).Format(time.RFC3339Nano), CandidateUpdatedAt: record.UpdatedAt, ConfigurationRevision: config.Revision, Options: map[string]string{"savepath": "/data"}}, Metainfo: []byte("synthetic-private-announce"), ObservedAt: time.Now()}
	for _, pool := range pools {
		result.Pools = append(result.Pools, models.RacingPoolBudget{StoragePoolID: pool, AvailableBytes: 100, ObservedAt: time.Now()})
	}
	return result
}

func TestRacingReservationSharedDiskAcrossStores(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			first := f.reservation(t, "event:a", "a", 70, f.pools[0])
			second := f.reservation(t, "event:b", "b", 70, f.pools[0])
			other, err := models.NewRacingStore(f.db, bytes.Repeat([]byte{9}, 32))
			require.NoError(t, err)
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			start := make(chan struct{})
			for i, input := range []models.RacingReservation{first, second} {
				wg.Go(func() {
					<-start
					store := f.store
					if i == 1 {
						store = other
					}
					errs <- store.ReserveAdd(t.Context(), input)
				})
			}
			close(start)
			wg.Wait()
			close(errs)
			success, rejected := 0, 0
			for err := range errs {
				if err == nil {
					success++
				} else {
					require.ErrorIs(t, err, models.ErrRacingCapacity)
					rejected++
				}
			}
			require.Equal(t, 1, success)
			require.Equal(t, 1, rejected)
			// An unrelated disk is not blocked by the rejected promise.
			independent := f.reservation(t, "event:c", "c", 70, f.pools[1])
			require.NoError(t, other.ReserveAdd(t.Context(), independent))
			list, err := other.AddIntents(t.Context(), "", 100)
			require.NoError(t, err)
			require.Len(t, list, 2)
		})
	}
}

func TestRacingIntentRestartAndUnknownRetainsReservation(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			input := f.reservation(t, "event:a", "a", 70, f.pools[0])
			input.Plan.Name = "Example Aurora"
			input.Plan.Decision = &models.RacingDecisionEvidence{Algorithm: "near-load-history-v1", Candidate: json.RawMessage(`{"item":{"title":"Example Aurora"}}`), Selection: json.RawMessage(`{"state":"ready"}`), Targets: []models.RacingDecisionTarget{{InstanceID: f.instance, AvailableBytes: 100, PoolIDs: input.Plan.PoolIDs}}}

			require.NoError(t, f.store.ReserveAdd(t.Context(), input))
			var cipher string
			require.NoError(t, f.db.QueryRowContext(t.Context(), "SELECT metainfo_ciphertext FROM racing_add_intents WHERE candidate_key=?", input.Plan.CandidateKey).Scan(&cipher))
			require.NotContains(t, cipher, "private-announce")
			require.NoError(t, f.store.SubmitAdd(t.Context(), currentIntent(t, f.store, input.Plan.CandidateKey), input.Pools, 0, time.Now()))
			// Simulate a process dying immediately after committing the send boundary.
			restart, err := models.NewRacingStore(f.db, bytes.Repeat([]byte{9}, 32))
			require.NoError(t, err)
			item, err := restart.AddIntent(t.Context(), input.Plan.CandidateKey)
			require.NoError(t, err)
			require.Equal(t, input.Plan.Decision, item.Plan.Decision)
			require.Equal(t, input.Plan.Name, item.Plan.Name)
			require.Equal(t, "submitted", item.State)
			require.ErrorIs(t, restart.SubmitAdd(t.Context(), currentIntent(t, restart, input.Plan.CandidateKey), input.Pools, 0, time.Now()), models.ErrRacingIntentState)
			require.ErrorIs(t, restart.CancelReserved(t.Context(), currentIntent(t, restart, input.Plan.CandidateKey)), models.ErrRacingIntentState)
			require.ErrorIs(t, restart.ReserveAdd(t.Context(), input), models.ErrRacingIntentExists)
			require.NoError(t, restart.RecordAddResult(t.Context(), input.Plan.CandidateKey, false))
			second := f.reservation(t, "event:b", "b", 40, f.pools[0])
			require.ErrorIs(t, restart.ReserveAdd(t.Context(), second), models.ErrRacingCapacity)
			raw, err := restart.IntentMetainfo(t.Context(), input.Plan.CandidateKey)
			require.NoError(t, err)
			require.Equal(t, input.Metainfo, raw)
			require.NoError(t, restart.ConfirmAdd(t.Context(), input.Plan.CandidateKey, time.Now(), false, false))
			item, err = restart.AddIntent(t.Context(), input.Plan.CandidateKey)
			require.NoError(t, err)
			require.Nil(t, item.RunnableAt)
			require.Nil(t, item.TransferredAt)
			// Remaining writes are now already included in a newer 60-byte budget.
			second.Pools[0].AvailableBytes = 60
			second.Pools[0].ObservedAt = time.Now()
			second.Pools[0].CoveredCandidates = []string{input.Plan.CandidateKey}
			require.NoError(t, restart.ReserveAdd(t.Context(), second))
			require.NoError(t, restart.ConfirmAdd(t.Context(), input.Plan.CandidateKey, time.Now(), true, true))
			require.NoError(t, restart.RecordAddResult(t.Context(), input.Plan.CandidateKey, false), "late transport failure cannot downgrade confirmed work")
			item, err = restart.AddIntent(t.Context(), input.Plan.CandidateKey)
			require.NoError(t, err)
			require.Equal(t, "confirmed", item.State)
			require.NotNil(t, item.RunnableAt)
			require.NotNil(t, item.TransferredAt)
			require.NoError(t, restart.InvalidateCandidate(t.Context(), input.Plan.CandidateKey, []byte(`{"state":"rejected","reason":"later_evidence"}`)))
			frozen, err := restart.AddIntent(t.Context(), input.Plan.CandidateKey)
			require.NoError(t, err)
			require.Equal(t, item.Plan, frozen.Plan, "later candidate changes must not rewrite submitted decision evidence")
		})
	}
}

func TestRacingIntentConfigurationAndEvidenceFences(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			input := f.reservation(t, "event:a", "a", 70, f.pools[0])
			require.NoError(t, f.store.ReserveAdd(t.Context(), input))
			_, err := f.store.SaveStoragePool(t.Context(), f.pools[1], models.RacingStoragePoolInput{Name: "Renamed disk"})
			require.NoError(t, err)
			require.ErrorIs(t, f.store.SubmitAdd(t.Context(), currentIntent(t, f.store, input.Plan.CandidateKey), input.Pools, 0, time.Now()), models.ErrRacingStale)
			require.NoError(t, f.store.CancelReserved(t.Context(), currentIntent(t, f.store, input.Plan.CandidateKey)))
			input = f.reservation(t, "event:a", "a", 70, f.pools[0])
			require.NoError(t, f.store.ReserveAdd(t.Context(), input))
			require.NoError(t, f.store.InvalidateCandidate(t.Context(), input.Plan.CandidateKey, []byte(`{"state":"rejected"}`)))
			require.ErrorIs(t, f.store.SubmitAdd(t.Context(), currentIntent(t, f.store, input.Plan.CandidateKey), input.Pools, 0, time.Now()), models.ErrRacingStale)
			require.NoError(t, f.store.CancelReserved(t.Context(), currentIntent(t, f.store, input.Plan.CandidateKey)))
			input = f.reservation(t, "event:a", "a", 70, f.pools[0])
			input.Plan.Deadline = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
			require.ErrorIs(t, f.store.ReserveAdd(t.Context(), input), models.ErrRacingStale)
			input = f.reservation(t, "event:a", "a", 70, f.pools[0])
			input.Pools[0].ObservedAt = time.Now().Add(-6 * time.Second)
			require.ErrorIs(t, f.store.ReserveAdd(t.Context(), input), models.ErrRacingStale)
			input = f.reservation(t, "event:a", "a", 70, f.pools[0])
			require.NoError(t, f.store.ReserveAdd(t.Context(), input))
			duplicate := f.reservation(t, "another-site-event", "a", 10, f.pools[1])
			require.ErrorIs(t, f.store.ReserveAdd(t.Context(), duplicate), models.ErrRacingIntentExists)
		})
	}
}

func TestRacingReservationMultipleDisksRollback(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			input := f.reservation(t, "event:a", "a", 70, f.pools...)
			input.Pools[1].AvailableBytes = 60
			require.ErrorIs(t, f.store.ReserveAdd(t.Context(), input), models.ErrRacingCapacity)
			list, err := f.store.AddIntents(t.Context(), "", 100)
			require.NoError(t, err)
			require.Empty(t, list)
			input.Pools[1].AvailableBytes = 100
			require.NoError(t, f.store.ReserveAdd(t.Context(), input))
			var count int
			require.NoError(t, f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM racing_space_commitments").Scan(&count))
			require.Equal(t, 2, count)
		})
	}
}

func TestRacingSubmissionWaitsForPendingEvidenceAndKeepsPoolIdentity(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			input := f.reservation(t, "event:a", "a", 70, f.pools[0])
			config, err := f.store.Configuration(t.Context())
			require.NoError(t, err)
			sources, err := f.store.RuntimeSources(t.Context())
			require.NoError(t, err)
			require.NoError(t, f.store.SaveDiscovery(t.Context(), sources[0], "event:a", []byte(`{}`), []byte(`{}`), true))
			after, err := f.store.Configuration(t.Context())
			require.NoError(t, err)
			require.Equal(t, config.Revision, after.Revision, "observations do not churn the configuration revision")
			require.ErrorIs(t, f.store.ReserveAdd(t.Context(), input), models.ErrRacingStale, "evaluate new evidence before reserving")
			pending, err := f.store.PendingDiscoveries(t.Context(), 0, 0, 100)
			require.NoError(t, err)
			records, err := f.store.CandidateRecords(t.Context(), "", nil, 100)
			require.NoError(t, err)
			require.NoError(t, f.store.SaveCandidate(t.Context(), records[0], pending.Items))
			records, err = f.store.CandidateRecords(t.Context(), "", nil, 100)
			require.NoError(t, err)
			input.Plan.CandidateUpdatedAt = records[0].UpdatedAt
			require.NoError(t, f.store.ReserveAdd(t.Context(), input))
			require.ErrorIs(t, f.store.Delete(t.Context(), "storage-pools", f.pools[0]), models.ErrRacingReferenced)
			require.NoError(t, f.store.SaveCandidateMetadata(t.Context(), input.Plan.CandidateKey, sources[0], []byte(`{}`), []byte("new-proof")))
			require.ErrorIs(t, f.store.SubmitAdd(t.Context(), currentIntent(t, f.store, input.Plan.CandidateKey), input.Pools, 0, time.Now()), models.ErrRacingStale, "new metainfo must be evaluated before submission")
		})
	}
}

func TestRacingExecutableCandidatesPrioritizeAcrossPages(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			for i := range 102 {
				key := fmt.Sprintf("event:%03d", i)
				priority := "normal"
				if i == 101 {
					priority = "official"
				}
				require.NoError(t, f.store.SaveCandidate(t.Context(), models.RacingCandidateRecord{Key: key, SiteID: f.site, EventKey: key, FirstSeenAt: time.Now().UTC().Format(time.RFC3339Nano), Candidate: json.RawMessage(`{}`), Selection: json.RawMessage(fmt.Sprintf(`{"priority":%q}`, priority)), State: "ready"}, nil))
			}
			first, err := f.store.ExecutableCandidates(t.Context(), 0, 100)
			require.NoError(t, err)
			require.Len(t, first, 100)
			require.Equal(t, "event:101", first[0].Key)
			tail, err := f.store.ExecutableCandidates(t.Context(), 100, 100)
			require.NoError(t, err)
			require.Len(t, tail, 2)
		})
	}
}

func currentIntent(t *testing.T, store *models.RacingStore, key string) models.RacingAddIntent {
	t.Helper()
	intent, err := store.AddIntent(t.Context(), key)
	require.NoError(t, err)
	return intent
}

func TestRacingStaleWorkerCannotSubmitOrCancelReplacement(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			input := f.reservation(t, "event:a", "a", 70, f.pools[0])
			require.NoError(t, f.store.ReserveAdd(t.Context(), input))
			old := currentIntent(t, f.store, input.Plan.CandidateKey)
			require.NoError(t, f.store.CancelReserved(t.Context(), old))
			require.NoError(t, f.store.ReserveAdd(t.Context(), input))
			replacement := currentIntent(t, f.store, input.Plan.CandidateKey)
			require.NotEqual(t, old.ReservedAt, replacement.ReservedAt)
			require.ErrorIs(t, f.store.CancelReserved(t.Context(), old), models.ErrRacingIntentState)
			require.ErrorIs(t, f.store.SubmitAdd(t.Context(), old, input.Pools, 0, time.Now()), models.ErrRacingIntentState)
			require.NoError(t, f.store.SubmitAdd(t.Context(), replacement, input.Pools, 0, time.Now()))
		})
	}
}

func TestRacingConfirmedRemovalReleasesForLaterRevivalButUnknownDoesNot(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			input := f.reservation(t, "event:a", "a", 70, f.pools[0])
			require.NoError(t, f.store.ReserveAdd(t.Context(), input))
			intent := currentIntent(t, f.store, input.Plan.CandidateKey)
			require.NoError(t, f.store.SubmitAdd(t.Context(), intent, input.Pools, 0, time.Now()))
			require.ErrorIs(t, f.store.ObserveConfirmedMissing(t.Context(), intent, time.Now()), models.ErrRacingIntentState, "a missing unconfirmed submission never releases its promise")
			require.NoError(t, f.store.ConfirmAdd(t.Context(), intent.CandidateKey, time.Now(), false, false))
			// Seed persisted successful and missing observations without sleeping through
			// the whole recovery window. Only fresh subsequent samples can retire it.
			stamp := func(age time.Duration) string { return time.Now().Add(-age).UTC().Format(time.RFC3339Nano) }
			_, err := f.db.ExecContext(t.Context(), "UPDATE racing_add_intents SET observed_at=?,missing_since=?,missing_observed_at=? WHERE candidate_key=?", stamp(time.Minute), stamp(40*time.Second), stamp(15*time.Second), intent.CandidateKey)
			require.NoError(t, err)
			require.NoError(t, f.store.ObserveConfirmedMissing(t.Context(), intent, time.Now()))
			require.Equal(t, "confirmed", currentIntent(t, f.store, intent.CandidateKey).State, "a sampling gap restarts the absence window")
			_, err = f.db.ExecContext(t.Context(), "UPDATE racing_add_intents SET missing_since=?,missing_observed_at=? WHERE candidate_key=?", stamp(40*time.Second), stamp(time.Second), intent.CandidateKey)
			require.NoError(t, err)
			require.NoError(t, f.store.ObserveConfirmedMissing(t.Context(), intent, time.Now()))
			require.Equal(t, "retired", currentIntent(t, f.store, intent.CandidateKey).State)
			require.ErrorIs(t, f.store.ReserveAdd(t.Context(), input), models.ErrRacingIntentExists, "retired original event is never replayed")
			revival := f.reservation(t, "event:a:revived", "a", 70, f.pools[0])
			require.NoError(t, f.store.ReserveAdd(t.Context(), revival), "a later revival is not permanently swallowed by history")
		})
	}
}

func TestRacingRepeatedEvidencePreservesReservationButNewProofFences(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			input := f.reservation(t, "event:unchanged", "a", 70, f.pools[0])
			require.NoError(t, f.store.ReserveAdd(t.Context(), input))
			rows, err := f.store.CandidateRecords(t.Context(), "", nil, 100)
			require.NoError(t, err)
			record := rows[0]
			require.NoError(t, f.store.SaveCandidate(t.Context(), record, nil))
			require.NoError(t, f.store.SubmitAdd(t.Context(), currentIntent(t, f.store, record.Key), input.Pools, 0, time.Now()))

			second := f.reservation(t, "event:new-proof", "b", 10, f.pools[1])
			require.NoError(t, f.store.ReserveAdd(t.Context(), second))
			rows, err = f.store.CandidateRecords(t.Context(), "", nil, 100)
			require.NoError(t, err)
			record = rows[0]
			require.Equal(t, second.Plan.CandidateKey, record.Key)
			sources, err := f.store.RuntimeSources(t.Context())
			require.NoError(t, err)
			require.NoError(t, f.store.SaveCandidateMetadata(t.Context(), record.Key, sources[0], []byte(`{}`), []byte("synthetic-refreshed-proof")))
			require.ErrorIs(t, f.store.SubmitAdd(t.Context(), currentIntent(t, f.store, record.Key), second.Pools, 0, time.Now()), models.ErrRacingStale)
			require.NoError(t, f.store.SaveCandidate(t.Context(), record, nil))
			rows, err = f.store.CandidateRecords(t.Context(), "", nil, 100)
			require.NoError(t, err)
			require.NotEqual(t, record.UpdatedAt, rows[0].UpdatedAt)
			require.ErrorIs(t, f.store.SubmitAdd(t.Context(), currentIntent(t, f.store, record.Key), second.Pools, 0, time.Now()), models.ErrRacingStale)
		})
	}
}
