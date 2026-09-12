// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestReclaimUploadWindowCoverageAndReset(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			store := models.NewAutomationStore(f.db)
			now := time.Now().UTC()
			sample := func(seconds int, added, uploaded int64) models.ReclaimUploadWindow {
				t.Helper()
				rows, err := store.CaptureReclaimUploads(t.Context(), f.instance, now.Add(time.Duration(seconds)*time.Second), time.Minute, 40*time.Second, []models.ReclaimUploadCounter{{Hash: "synthetic", AddedOn: added, Uploaded: uploaded}})
				require.NoError(t, err)
				return rows["synthetic"]
			}
			require.False(t, sample(0, 1, 100).Covered)
			require.False(t, sample(20, 1, 120).Covered)
			require.False(t, sample(40, 1, 140).Covered)
			// Reopening the store reads persisted samples; no in-memory timer supplies coverage.
			store = models.NewAutomationStore(f.db)
			result := sample(60, 1, 160)
			require.True(t, result.Covered)
			require.EqualValues(t, 60, result.Bytes)
			require.Equal(t, result, sample(60, 1, 160), "same sync sample does not add time")
			require.False(t, sample(61, 2, 0).Covered, "new task generation resets")
			require.False(t, sample(80, 2, 20).Covered)
			require.False(t, sample(100, 2, 10).Covered, "counter regression resets")
			require.False(t, sample(141, 2, 30).Covered, "gap invalidates coverage")
			require.False(t, sample(161, 2, 40).Covered)
			require.False(t, sample(181, 2, 50).Covered)
			require.True(t, sample(201, 2, 60).Covered)
			_, err := store.CaptureReclaimUploads(t.Context(), f.instance, now.Add(202*time.Second), time.Minute, 40*time.Second, nil)
			require.NoError(t, err)
			require.False(t, sample(203, 2, 60).Covered, "missing task invalidates window")
		})
	}
}
