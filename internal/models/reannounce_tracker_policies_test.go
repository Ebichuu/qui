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

func TestTrackerPolicyWaitsAndAccountBinding(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			_, err := f.store.SaveSite(t.Context(), f.site, models.RacingSiteInput{Name: "Synthetic account", BaseURL: "https://example.invalid", Enabled: true, TrackerHosts: []string{"a.example.invalid"}, RequestIntervalSeconds: 1})
			require.NoError(t, err)
			store := models.NewInstanceReannounceStore(f.db)
			policy := models.ReannounceTrackerPolicy{TrackerKey: strings.Repeat("a", 64), SiteID: f.site, TrackerHost: "a.example.invalid", IntervalSeconds: 10, WaitMessageDigest: strings.Repeat("b", 64), WaitSeconds: 20, DeleteProtection: "accounted"}
			require.NoError(t, store.SaveTrackerPolicy(t.Context(), f.instance, policy))
			rows, err := store.TrackerPolicies(t.Context(), f.instance)
			require.NoError(t, err)
			require.Equal(t, []models.ReannounceTrackerPolicy{policy}, rows)
			policy.TrackerHost = "b.example.invalid"
			require.ErrorIs(t, store.SaveTrackerPolicy(t.Context(), f.instance, policy), models.ErrTrackerPolicyInvalid)
			now := time.Now().UTC()
			deadline, err := store.ObserveTrackerWait(t.Context(), f.instance, "abc", 100, "key", "message:0", now, 20*time.Second)
			require.NoError(t, err)
			require.True(t, deadline.Equal(now.Add(20*time.Second)))
			reopened := models.NewInstanceReannounceStore(f.db)
			same, err := reopened.ObserveTrackerWait(t.Context(), f.instance, "abc", 100, "key", "message:0", now.Add(5*time.Second), 20*time.Second)
			require.NoError(t, err)
			require.True(t, same.Equal(deadline))
			shorter, err := reopened.ObserveTrackerWait(t.Context(), f.instance, "abc", 100, "key", "message:0", now.Add(6*time.Second), time.Second)
			require.NoError(t, err)
			require.True(t, shorter.Equal(deadline))
			longer, err := reopened.ObserveTrackerWait(t.Context(), f.instance, "abc", 100, "key", "message:0", now.Add(7*time.Second), 30*time.Second)
			require.NoError(t, err)
			require.True(t, longer.Equal(now.Add(30*time.Second)))
			again, err := reopened.ObserveTrackerWait(t.Context(), f.instance, "abc", 100, "key", "message:1", now.Add(40*time.Second), 20*time.Second)
			require.NoError(t, err)
			require.True(t, again.Equal(now.Add(60*time.Second)))
			_, err = reopened.ObserveTrackerWait(t.Context(), f.instance, "abc", 100, "key", "older", now, time.Second)
			require.Error(t, err)
			generation, err := reopened.ObserveTrackerWait(t.Context(), f.instance, "abc", 200, "key", "message:1", now.Add(41*time.Second), 20*time.Second)
			require.NoError(t, err)
			require.True(t, generation.Equal(now.Add(61*time.Second)))
			_, err = f.store.SaveSite(t.Context(), f.site, models.RacingSiteInput{Name: "Synthetic account", BaseURL: "https://example.invalid", Enabled: false, TrackerHosts: []string{"a.example.invalid"}, RequestIntervalSeconds: 1})
			require.NoError(t, err)
			_, err = store.TrackerPolicies(t.Context(), f.instance)
			require.ErrorIs(t, err, models.ErrTrackerPolicyInvalid)
		})
	}
}
