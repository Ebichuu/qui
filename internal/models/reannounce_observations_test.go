// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestTrackerObservationsPersistAndRejectOlderSnapshots(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			store := models.NewInstanceReannounceStore(f.db)
			now := time.Now().UTC()
			item := models.ReannounceObservation{InstanceID: f.instance, Hash: "abc", AddedOn: 100, ObservedAt: now, LocalObservedAt: now.Add(-time.Second), LocalUploaded: 42, Trackers: []models.TrackerObservation{{Key: "synthetic-key", Host: "example.invalid", State: "error_unknown"}}}
			require.NoError(t, store.SaveObservation(t.Context(), item))
			reopened := models.NewInstanceReannounceStore(f.db)
			rows, err := reopened.Observations(t.Context(), f.instance)
			require.NoError(t, err)
			require.Equal(t, []models.ReannounceObservation{item}, rows)
			require.Error(t, store.SaveObservation(t.Context(), item))
			item.ObservedAt = now.Add(time.Second)
			item.AddedOn = 200
			item.Trackers = []models.TrackerObservation{}
			require.NoError(t, store.SaveObservation(t.Context(), item))
			rows, err = reopened.Observations(t.Context(), f.instance)
			require.NoError(t, err)
			require.Equal(t, []models.ReannounceObservation{item}, rows)
			old := item
			old.ObservedAt = now
			old.AddedOn = 100
			require.Error(t, store.SaveObservation(t.Context(), old))
			rows, err = reopened.Observations(t.Context(), f.instance)
			require.NoError(t, err)
			require.Equal(t, []models.ReannounceObservation{item}, rows)
		})
	}
}
