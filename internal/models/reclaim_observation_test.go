// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestReclaimObservationIsolationAndRollback(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			ctx := t.Context()
			var rule int
			require.NoError(t, f.db.QueryRowContext(ctx, `INSERT INTO automations(instance_id,name,tracker_pattern,conditions) VALUES(?,?,?,?) RETURNING id`, f.instance, "Synthetic candidate", "*", `{}`).Scan(&rule))
			store := models.NewAutomationStore(f.db)
			item := models.AutomationConditionObservation{RuleID: rule, RuleVersion: strings.Repeat("a", 64), Hash: "synthetic", AddedOn: 100, DryRun: true, Elapsed: time.Minute, Duration: time.Minute, ObservedAt: time.Now().UTC(), Uploaded: 1}
			require.NoError(t, store.SaveReclaimObservations(ctx, f.instance, []models.AutomationConditionObservation{item}))
			daily, err := store.ConditionObservations(ctx, f.instance)
			require.NoError(t, err)
			require.Empty(t, daily)
			invalid := item
			invalid.Hash = ""
			require.Error(t, store.SaveReclaimObservations(ctx, f.instance, []models.AutomationConditionObservation{invalid}))
			rows, err := store.ReclaimObservations(ctx, f.instance)
			require.NoError(t, err)
			require.Equal(t, []models.AutomationConditionObservation{item}, rows)
			require.NoError(t, store.SaveConditionObservations(ctx, f.instance, nil))
			rows, err = store.ReclaimObservations(ctx, f.instance)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.NoError(t, store.SaveReclaimObservations(ctx, f.instance, nil))
			rows, err = store.ReclaimObservations(ctx, f.instance)
			require.NoError(t, err)
			require.Empty(t, rows)
		})
	}
}
