// Copyright (c) 2025-2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package automations

import (
	"testing"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func durationTestRule(durationSeconds int, updatedAt time.Time) *models.Automation {
	return &models.Automation{
		ID:        7,
		UpdatedAt: updatedAt,
		Conditions: &models.ActionConditions{
			Delete: &models.DeleteAction{
				Enabled:                       true,
				ConditionMatchDurationSeconds: durationSeconds,
			},
		},
	}
}

func TestDeleteConditionReadyRequiresContinuousMatch(t *testing.T) {
	service := &Service{}
	start := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	rule := durationTestRule(60, start)
	key := newDeleteConditionMatchKey(3, rule, qbt.Torrent{Hash: "abc"}, false)

	if service.deleteConditionReady(start, key, time.Minute, true) {
		t.Fatal("first match should start the timer, not delete")
	}
	if service.deleteConditionReady(start.Add(59*time.Second), key, time.Minute, true) {
		t.Fatal("match should not be ready before the duration elapses")
	}
	if !service.deleteConditionReady(start.Add(time.Minute), key, time.Minute, true) {
		t.Fatal("match should be ready once the full duration elapses")
	}

	if service.deleteConditionReady(start.Add(61*time.Second), key, time.Minute, false) {
		t.Fatal("a non-match should reset the timer")
	}
	if service.deleteConditionReady(start.Add(2*time.Minute), key, time.Minute, true) {
		t.Fatal("the first match after a reset should start a new timer")
	}
}

func TestDeleteConditionReadyWithoutDurationUsesCurrentMatch(t *testing.T) {
	service := &Service{}
	key := deleteConditionMatchKey{instanceID: 1, ruleID: 2, hash: "abc"}
	now := time.Now()

	if !service.deleteConditionReady(now, key, 0, true) {
		t.Fatal("matched condition should apply immediately when duration is disabled")
	}
	if service.deleteConditionReady(now, key, 0, false) {
		t.Fatal("unmatched condition should not apply")
	}
}

func TestDeleteConditionMonitoringRuleOnlyKeepsDeleteAction(t *testing.T) {
	rule := durationTestRule(60, time.Now())
	rule.Conditions.Grouping = &models.GroupingConfig{DefaultGroupID: "content"}
	rule.Conditions.Pause = &models.PauseAction{Enabled: true}

	monitoringRule := deleteConditionMonitoringRule(rule)
	require.NotNil(t, monitoringRule)
	require.NotSame(t, rule, monitoringRule)
	require.NotSame(t, rule.Conditions, monitoringRule.Conditions)
	require.Same(t, rule.Conditions.Delete, monitoringRule.Conditions.Delete)
	require.Same(t, rule.Conditions.Grouping, monitoringRule.Conditions.Grouping)
	require.Nil(t, monitoringRule.Conditions.Pause)
	require.Equal(t, rule.ID, monitoringRule.ID)
	require.Equal(t, rule.UpdatedAt, monitoringRule.UpdatedAt)
}

func TestDeleteConditionReadyForRuleTracksWhileSuppressed(t *testing.T) {
	service := &Service{}
	start := time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)
	rule := durationTestRule(60, start)
	torrent := qbt.Torrent{Hash: "abc"}
	suppressed := map[int]struct{}{rule.ID: {}}

	firstSeen := make(map[deleteConditionMatchKey]struct{})
	if service.deleteConditionReadyForRule(start, 3, rule, torrent, false, true, suppressed, firstSeen) {
		t.Fatal("the first match should only start the timer")
	}
	require.Len(t, firstSeen, 1)

	secondSeen := make(map[deleteConditionMatchKey]struct{})
	if service.deleteConditionReadyForRule(start.Add(time.Minute), 3, rule, torrent, false, true, suppressed, secondSeen) {
		t.Fatal("cooldown should suppress deletion after the duration elapses")
	}
	require.Len(t, secondSeen, 1)

	thirdSeen := make(map[deleteConditionMatchKey]struct{})
	if !service.deleteConditionReadyForRule(start.Add(2*time.Minute), 3, rule, torrent, false, true, nil, thirdSeen) {
		t.Fatal("the retained timer should allow deletion after cooldown ends")
	}
}

func TestDeleteConditionReadyForRuleResetsWhileSuppressed(t *testing.T) {
	service := &Service{}
	start := time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)
	rule := durationTestRule(60, start)
	torrent := qbt.Torrent{Hash: "abc"}
	suppressed := map[int]struct{}{rule.ID: {}}

	service.deleteConditionReadyForRule(start, 3, rule, torrent, false, true, suppressed, make(map[deleteConditionMatchKey]struct{}))
	service.deleteConditionReadyForRule(start.Add(time.Minute), 3, rule, torrent, false, false, suppressed, make(map[deleteConditionMatchKey]struct{}))

	if service.deleteConditionReadyForRule(start.Add(2*time.Minute), 3, rule, torrent, false, true, nil, make(map[deleteConditionMatchKey]struct{})) {
		t.Fatal("a non-match during cooldown should reset the timer")
	}
}

func TestPruneDeleteConditionMatchesResetsUnobservedAndOldVersions(t *testing.T) {
	service := &Service{deleteConditionMatches: make(map[deleteConditionMatchKey]deleteConditionMatchState)}
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	rule := durationTestRule(60, now)
	current := newDeleteConditionMatchKey(3, rule, qbt.Torrent{Hash: "current"}, false)
	unobserved := newDeleteConditionMatchKey(3, rule, qbt.Torrent{Hash: "unobserved"}, false)
	oldVersion := unobserved
	oldVersion.hash = "old-version"
	oldVersion.ruleVersion--
	otherInstance := current
	otherInstance.instanceID = 4

	for _, key := range []deleteConditionMatchKey{current, unobserved, oldVersion, otherInstance} {
		service.deleteConditionMatches[key] = deleteConditionMatchState{matchedSince: now, lastSeen: now}
	}

	service.pruneDeleteConditionMatches(3, []*models.Automation{rule}, false, map[deleteConditionMatchKey]struct{}{current: {}})

	if _, ok := service.deleteConditionMatches[current]; !ok {
		t.Fatal("observed current match should be retained")
	}
	if _, ok := service.deleteConditionMatches[unobserved]; ok {
		t.Fatal("unobserved match should be reset")
	}
	if _, ok := service.deleteConditionMatches[oldVersion]; ok {
		t.Fatal("match from an older rule version should be reset")
	}
	if _, ok := service.deleteConditionMatches[otherInstance]; !ok {
		t.Fatal("another instance should not be pruned")
	}
}

func TestCleanupStaleEntriesPreservesLongDurationTimers(t *testing.T) {
	now := time.Now()
	longDurationKey := deleteConditionMatchKey{instanceID: 1, ruleID: 2, hash: "long"}
	shortDurationKey := deleteConditionMatchKey{instanceID: 1, ruleID: 3, hash: "short"}
	service := &Service{
		deleteConditionMatches: map[deleteConditionMatchKey]deleteConditionMatchState{
			longDurationKey: {
				matchedSince: now.Add(-25 * time.Hour),
				lastSeen:     now.Add(-25 * time.Hour),
				duration:     48 * time.Hour,
			},
			shortDurationKey: {
				matchedSince: now.Add(-25 * time.Hour),
				lastSeen:     now.Add(-25 * time.Hour),
				duration:     time.Minute,
			},
		},
	}

	service.cleanupStaleEntries()

	if _, ok := service.deleteConditionMatches[longDurationKey]; !ok {
		t.Fatal("cleanup should preserve a timer whose configured duration is still active")
	}
	if _, ok := service.deleteConditionMatches[shortDurationKey]; ok {
		t.Fatal("cleanup should remove a stale short-duration timer")
	}
}

func TestProcessTorrentsDefersDeleteUntilDurationGateIsReady(t *testing.T) {
	rule := durationTestRule(60, time.Now())
	rule.TrackerPattern = "*"
	rule.Enabled = true
	rule.Conditions.Delete.Mode = models.DeleteModeWithFiles
	rule.Conditions.Delete.Condition = &models.RuleCondition{
		Field:    models.FieldUpSpeed,
		Operator: models.OperatorLessThan,
		Value:    "51200",
	}
	torrent := qbt.Torrent{Hash: "abc", Name: "Example.Release", UpSpeed: 1024}
	gateCalls := 0
	evalCtx := &EvalContext{
		DeleteConditionGate: func(_ *models.Automation, _ qbt.Torrent, matched bool) bool {
			if !matched {
				t.Fatal("raw delete condition should match")
			}
			gateCalls++
			return gateCalls >= 2
		},
	}

	states := processTorrents([]qbt.Torrent{torrent}, []*models.Automation{rule}, evalCtx, nil, nil, nil, nil)
	if len(states) != 0 {
		t.Fatal("delete should be deferred while the duration gate is waiting")
	}

	states = processTorrents([]qbt.Torrent{torrent}, []*models.Automation{rule}, evalCtx, nil, nil, nil, nil)
	state, ok := states[torrent.Hash]
	if !ok || !state.shouldDelete {
		t.Fatal("delete should apply once the duration gate is ready")
	}
}
