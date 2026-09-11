// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package automations

import (
	"bytes"
	"testing"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/database"
	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestConditionPersistenceAcrossRestart(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var db *database.DB
			if engine == "sqlite" {
				db = testdb.NewMigratedSQLite(t, "condition-persistence")
			} else {
				db = testdb.NewMigratedPostgres(t, "condition-persistence")
			}
			instances, err := models.NewInstanceStore(db, bytes.Repeat([]byte{4}, 32))
			require.NoError(t, err)
			instance, err := instances.Create(t.Context(), "Synthetic observer", "http://127.0.0.1:1", "synthetic", "synthetic", nil, nil, false, nil)
			require.NoError(t, err)
			var id int
			require.NoError(t, db.QueryRowContext(t.Context(), `INSERT INTO automations(instance_id,name,tracker_pattern,conditions) VALUES (?,?,?,?) RETURNING id`, instance.ID, "Synthetic duration", "*", "{}").Scan(&id))
			store := models.NewAutomationStore(db)
			start := time.Now().UTC()
			rule := durationTestRule(60, start)
			rule.ID = id
			interval := 30
			rule.IntervalSeconds = &interval
			torrent := qbt.Torrent{Hash: "synthetic-hash", AddedOn: 123, Uploaded: 20, Downloaded: 40}
			version := deleteConditionRuleVersion(rule)
			seen := map[deleteConditionMatchKey]struct{}{}
			original := &Service{ruleStore: store}
			require.False(t, original.deleteConditionReadyForRule(start, instance.ID, rule, torrent, false, true, nil, seen, version))
			require.False(t, original.deleteConditionReadyForRule(start.Add(40*time.Second), instance.ID, rule, torrent, false, true, nil, seen, version))
			require.NoError(t, original.checkpointConditionObservations(t.Context(), instance.ID))
			require.NoError(t, store.RecordDeleteCooldown(t.Context(), instance.ID, start))

			restarted := &Service{ruleStore: store}
			require.NoError(t, restarted.restoreConditionObservations(t.Context(), instance.ID))
			require.True(t, restarted.lastFreeSpaceDeleteAt[instance.ID].Equal(start))
			// Twenty seconds offline do not complete a sixty-second observation.
			require.False(t, restarted.deleteConditionReadyForRule(start.Add(60*time.Second), instance.ID, rule, torrent, false, true, nil, seen, version))
			require.True(t, restarted.deleteConditionReadyForRule(start.Add(80*time.Second), instance.ID, rule, torrent, false, true, nil, seen, version))

			longGap := &Service{ruleStore: store}
			require.NoError(t, longGap.restoreConditionObservations(t.Context(), instance.ID))
			require.False(t, longGap.deleteConditionReadyForRule(start.Add(110*time.Second), instance.ID, rule, torrent, false, true, nil, seen, version))
			key := newDeleteConditionMatchKey(instance.ID, rule, torrent, false, version)
			require.Equal(t, start.Add(110*time.Second), longGap.deleteConditionMatches[key].matchedSince)

			reset := &Service{ruleStore: store}
			require.NoError(t, reset.restoreConditionObservations(t.Context(), instance.ID))
			torrent.Uploaded = 0
			require.False(t, reset.deleteConditionReadyForRule(start.Add(50*time.Second), instance.ID, rule, torrent, false, true, nil, seen, version))
			require.Equal(t, start.Add(50*time.Second), reset.deleteConditionMatches[key].matchedSince)

			rows, err := store.ConditionObservations(t.Context(), instance.ID)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			invalid := rows[0]
			invalid.Duration = 0
			require.Error(t, store.SaveConditionObservations(t.Context(), instance.ID, []models.AutomationConditionObservation{invalid}))
			preserved, err := store.ConditionObservations(t.Context(), instance.ID)
			require.NoError(t, err)
			require.Equal(t, rows, preserved, "failed checkpoint must roll back deletion")
			if engine == "sqlite" {
				_, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_observation_delete BEFORE DELETE ON automation_condition_observations BEGIN SELECT RAISE(FAIL, 'synthetic checkpoint failure'); END`)
				require.NoError(t, err)
				failed := &Service{ruleStore: store}
				require.ErrorContains(t, failed.applyForInstance(t.Context(), instance.ID, true), "synthetic checkpoint failure")
				require.Empty(t, failed.deleteConditionMatches, "failed invalidation must discard restored maturity in memory")
				_, err = db.ExecContext(t.Context(), `DROP TRIGGER reject_observation_delete`)
				require.NoError(t, err)
			}
			require.NoError(t, store.SaveConditionObservations(t.Context(), instance.ID, nil))
			rows, err = store.ConditionObservations(t.Context(), instance.ID)
			require.NoError(t, err)
			require.Empty(t, rows)
		})
	}
}

func TestInstanceRunLockSeparatesInstances(t *testing.T) {
	service := &Service{}
	unlock := service.lockInstanceRun(1)
	released := false
	defer func() {
		if !released {
			unlock()
		}
	}()
	entered := make(chan struct{})
	done := make(chan struct{})
	go func() { release := service.lockInstanceRun(1); close(entered); release(); close(done) }()
	other := service.lockInstanceRun(2)
	other()
	select {
	case <-entered:
		t.Fatal("same instance entered concurrently")
	default:
	}
	unlock()
	released = true
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waiting instance did not resume")
	}
}
