// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestReclaimAssessmentRevisionAndPersistence(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			reservation := f.reservation(t, "assessment:a", "a", 100, f.pools[0])
			item := models.RacingReclaimAssessment{CandidateKey: reservation.Plan.CandidateKey, InstanceID: f.instance, ConfigurationRevision: reservation.Plan.ConfigurationRevision, Assessment: json.RawMessage(`{"state":"insufficient_evidence_or_budget","selected":[]}`), ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			require.NoError(t, f.store.SaveReclaimAssessment(t.Context(), item))
			rows, err := f.store.ReclaimAssessments(t.Context(), 100)
			require.NoError(t, err)
			require.Equal(t, []models.RacingReclaimAssessment{item}, rows)
			item.ConfigurationRevision--
			require.ErrorIs(t, f.store.SaveReclaimAssessment(t.Context(), item), models.ErrRacingStale)
			rows, err = f.store.ReclaimAssessments(t.Context(), 100)
			require.NoError(t, err)
			require.Equal(t, reservation.Plan.ConfigurationRevision, rows[0].ConfigurationRevision)
			intents, err := f.store.AddIntents(t.Context(), "", 100)
			require.NoError(t, err)
			require.Empty(t, intents)
			deletes, err := f.store.PendingAutomaticDeletes(t.Context(), f.instance)
			require.NoError(t, err)
			require.Empty(t, deletes)
		})
	}
}
