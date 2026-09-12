// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestReclaimCompleteCombinationBeforeAnyAction(t *testing.T) {
	now := time.Now()
	policy := models.RacingReclaimPolicy{Enabled: true, MaxDeletes: 3, MaxReclaimBytes: 500, MaxRecentUploadBytes: 30, MaxOvershootBytes: 50, RecentUploadWindowSeconds: 3600}
	candidates := []ReclaimEvidence{
		{Hash: "a", AddedOn: 1, InstanceID: 1, PoolID: 2, PhysicalBytes: 150, RecentUploadBytes: 1, LowEfficiencyDuration: time.Hour, ObservedAt: now, CapacityKnown: true, UploadWindowCovered: true},
		{Hash: "b", AddedOn: 1, InstanceID: 1, PoolID: 2, PhysicalBytes: 270, RecentUploadBytes: 2, LowEfficiencyDuration: time.Hour, ObservedAt: now, CapacityKnown: true, UploadWindowCovered: true},
		{Hash: "c", AddedOn: 1, InstanceID: 1, PoolID: 2, PhysicalBytes: 100, RecentUploadBytes: 20, LowEfficiencyDuration: time.Hour, ObservedAt: now, CapacityKnown: true, UploadWindowCovered: true},
	}
	result := assessReclaim(1, 2, 691, 300, policy, policy, ReclaimSpent{}, candidates, now)
	require.Equal(t, "assessed", result.State)
	require.EqualValues(t, 391, result.DeficitBytes)
	require.EqualValues(t, 420, result.PhysicalBytes)
	require.Len(t, result.Selected, 2)
	candidates[1].Protected = true
	result = assessReclaim(1, 2, 691, 300, policy, policy, ReclaimSpent{}, candidates, now)
	require.Equal(t, "insufficient_evidence_or_budget", result.State)
	require.Empty(t, result.Selected, "insufficient total never returns a partial deletion list")
}

func TestReclaimFrozenAndConsumedBudgets(t *testing.T) {
	now := time.Now()
	policy := models.RacingReclaimPolicy{Enabled: true, MaxDeletes: 1, MaxReclaimBytes: 100, MaxRecentUploadBytes: 5, MaxOvershootBytes: 10}
	candidate := []ReclaimEvidence{{Hash: "a", AddedOn: 1, InstanceID: 1, PoolID: 2, PhysicalBytes: 100, RecentUploadBytes: 3, LowEfficiencyDuration: time.Hour, ObservedAt: now, CapacityKnown: true, UploadWindowCovered: true}}
	raised := policy
	raised.MaxDeletes = 10
	raised.MaxReclaimBytes = 1000
	result := assessReclaim(1, 2, 90, 0, policy, raised, ReclaimSpent{Deletes: 1, CapacityBytes: 100, RecentUploadBytes: 3}, candidate, now)
	require.Equal(t, "budget_exhausted", result.State)
	lowered := policy
	lowered.MaxRecentUploadBytes = 2
	result = assessReclaim(1, 2, 90, 0, policy, lowered, ReclaimSpent{}, candidate, now)
	require.Empty(t, result.Selected)
	result = assessReclaim(1, 2, 90, 0, policy, policy, ReclaimSpent{OvershootBytes: 1}, candidate, now)
	require.Empty(t, result.Selected, "old excess remains spent")
	candidate[0].CapacityKnown = false
	result = assessReclaim(1, 2, 90, 0, policy, policy, ReclaimSpent{}, candidate, now)
	require.Empty(t, result.Selected)
	require.Equal(t, 1, result.UnknownCapacityCandidates)
}

func TestReclaimDeficitIncludesOldOvercommitment(t *testing.T) {
	deficit, ok := reclaimDeficit(100, -50)
	require.True(t, ok)
	require.EqualValues(t, 150, deficit)
	_, ok = reclaimDeficit(math.MaxInt64, -1)
	require.False(t, ok)
	deficit, ok = reclaimDeficit(100, 150)
	require.True(t, ok)
	require.Zero(t, deficit)
}
