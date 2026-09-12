// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package automations

import (
	"testing"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestReclaimRuleExplicitUseAndIsolation(t *testing.T) {
	for _, usage := range []string{"", "daily", "official", "both"} {
		t.Run(usage, func(t *testing.T) {
			source := &models.Automation{ID: 7, InstanceID: 1, Enabled: true, Conditions: &models.ActionConditions{
				Delete: &models.DeleteAction{Usage: usage, Enabled: true, Mode: models.DeleteModeWithFiles, ConditionMatchDurationSeconds: 60,
					Condition:    &models.RuleCondition{Field: FieldUpSpeed, Operator: OperatorLessThan, Value: "10"},
					DailyTrigger: &models.RuleCondition{Field: FieldFreeSpace, Operator: OperatorLessThan, Value: "100"}},
				Pause: &models.PauseAction{Enabled: true},
			}}
			candidate := reclaimRule(source, 2)
			if usage == "" || usage == "daily" {
				require.Nil(t, candidate)
				return
			}
			require.NotNil(t, candidate)
			require.Equal(t, 2, candidate.InstanceID)
			require.Equal(t, source.ID, candidate.ID)
			require.True(t, candidate.DryRun)
			require.Nil(t, candidate.Conditions.Pause)
			require.Nil(t, candidate.Conditions.Delete.DailyTrigger)
			require.NotNil(t, source.Conditions.Delete.DailyTrigger)
			require.Equal(t, usage, source.Conditions.Delete.Usage)
			require.Equal(t, 1, source.InstanceID)
		})
	}
}

func TestOfficialOnlyDoesNotDeleteInDailyProcessor(t *testing.T) {
	rule := &models.Automation{ID: 1, Conditions: &models.ActionConditions{
		Delete: &models.DeleteAction{Usage: "official", Enabled: true, Condition: &models.RuleCondition{Field: FieldUpSpeed, Operator: OperatorLessThan, Value: "10"}},
		Pause:  &models.PauseAction{Enabled: true},
	}}
	state := &torrentDesiredState{}
	processRuleForTorrent(rule, qbt.Torrent{Hash: "test", UpSpeed: 0}, state, nil, nil, nil, nil, nil, nil)
	require.False(t, state.shouldDelete)
}

func TestReclaimRuleRejectsMixedTriggerAndUnboundedObservation(t *testing.T) {
	for _, field := range []models.ConditionField{FieldFreeSpace, FieldUpSpeed} {
		for _, duration := range []int{0, 60} {
			rule := &models.Automation{Enabled: true, Conditions: &models.ActionConditions{Delete: &models.DeleteAction{
				Usage: "both", Enabled: true, Mode: models.DeleteModeWithFiles, ConditionMatchDurationSeconds: duration,
				Condition: &models.RuleCondition{Field: field, Operator: OperatorLessThan, Value: "10"},
			}}}
			if field == FieldFreeSpace || duration == 0 {
				require.Nil(t, reclaimRule(rule, 1))
			} else {
				require.NotNil(t, reclaimRule(rule, 1))
			}
		}
	}
}
