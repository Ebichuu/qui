// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestReannounceIntervalPersistsAcrossCompetingSenders(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			store := models.NewInstanceReannounceStore(f.db)
			now := time.Now().UTC()
			var accepted atomic.Int32
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					ok, err := store.BeginReannounce(t.Context(), f.instance, "abc", now, 10*time.Second)
					if err != nil {
						t.Error(err)
						return
					}
					if ok {
						accepted.Add(1)
					}
				})
			}
			wg.Wait()
			require.EqualValues(t, 1, accepted.Load())
			reopened := models.NewInstanceReannounceStore(f.db)
			ok, err := reopened.BeginReannounce(t.Context(), f.instance, "ABC", now.Add(5*time.Second), time.Second)
			require.NoError(t, err)
			require.False(t, ok)
			ok, err = reopened.BeginReannounce(t.Context(), f.instance, "abc", now.Add(10*time.Second), 20*time.Second)
			require.NoError(t, err)
			require.False(t, ok)
			ok, err = reopened.BeginReannounce(t.Context(), f.instance, "abc", now.Add(20*time.Second), 20*time.Second)
			require.NoError(t, err)
			require.True(t, ok)
		})
	}
}
