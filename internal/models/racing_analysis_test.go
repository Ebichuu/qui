// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestRacingAnalysisHistoryAndScheduling(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			ctx := t.Context()
			now := time.Now().UTC()
			reservation := f.reservation(t, "event:analysis", "a", 10, f.pools[0])
			require.NoError(t, f.store.ReserveAdd(ctx, reservation))
			// Seed a confirmed timestamp at the existing durable intent boundary.
			_, err := f.db.ExecContext(ctx, `UPDATE racing_add_intents SET state='confirmed',confirmed_at=? WHERE candidate_key=?`, now.Add(-time.Minute).Format(time.RFC3339Nano), reservation.Plan.CandidateKey)
			require.NoError(t, err)
			next, err := f.store.AnalysisNext(ctx, now)
			require.NoError(t, err)
			require.Nil(t, next, "default is disabled")
			history, err := f.store.AnalysisHistory(ctx, reservation.Plan.CandidateKey)
			require.NoError(t, err)
			require.NotNil(t, history)
			require.GreaterOrEqual(t, history.ExpectedSamples, 12)
			require.Equal(t, history.ExpectedSamples, history.MissingSamples, "never sampled still belongs in denominator")
			require.NoError(t, f.store.SaveAnalysisSetting(ctx, models.RacingAnalysisSetting{InstanceID: f.instance, Enabled: true}))
			next, err = f.store.AnalysisNext(ctx, now)
			require.NoError(t, err)
			require.Equal(t, reservation.Plan.CandidateKey, next.CandidateKey)
			history.Samples = append(history.Samples, models.RacingAnalysisSample{At: now, State: "peers_unavailable"})
			history.Peers["192.0.2.1:6881"] = models.RacingPeerHistory{IP: "192.0.2.1", Port: 6881, ASN: &models.RacingASNObservation{State: "found", Number: 64512, Organization: "Synthetic ASN", Network: "192.0.2.0/24", ObservedAt: now, DatabaseBuiltAt: now.Add(-time.Hour), DatabaseSHA256: "synthetic-version"}}
			require.NoError(t, f.store.SaveAnalysisHistory(ctx, *history))
			next, err = f.store.AnalysisNext(ctx, now.Add(time.Second))
			require.NoError(t, err)
			require.Nil(t, next, "failed requests also honor spacing")
			next, err = f.store.AnalysisNext(ctx, now.Add(6*time.Second))
			require.NoError(t, err)
			require.NotNil(t, next)
			old := *history
			old.Samples = []models.RacingAnalysisSample{{At: now.Add(-time.Second), State: "observed"}}
			require.NoError(t, f.store.SaveAnalysisHistory(ctx, old))
			restored, err := f.store.AnalysisHistory(ctx, reservation.Plan.CandidateKey)
			require.NoError(t, err)
			require.Equal(t, "peers_unavailable", restored.Samples[0].State)
			require.Equal(t, history.Peers["192.0.2.1:6881"].ASN, restored.Peers["192.0.2.1:6881"].ASN)
			require.Equal(t, "available", restored.ASNState)
			require.NoError(t, f.store.SaveAnalysisSetting(ctx, models.RacingAnalysisSetting{InstanceID: f.instance}))
			next, err = f.store.AnalysisNext(ctx, now.Add(6*time.Second))
			require.NoError(t, err)
			require.Nil(t, next)
			require.NoError(t, f.store.PruneAnalysisHistory(ctx, now.Add(31*24*time.Hour)))
			var count int
			require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM racing_analysis_history`).Scan(&count))
			require.Zero(t, count)
		})
	}
}

func TestRacingAnalysisExpiredHistoryBeforeCleanup(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			f := newRacingExecutionFixture(t, engine)
			ctx := t.Context()
			now := time.Now().UTC()
			old := now.Add(-models.RacingAnalysisRetention - time.Hour)
			reservation := f.reservation(t, "event:expired-analysis", "a", 10, f.pools[0])
			require.NoError(t, f.store.ReserveAdd(ctx, reservation))
			key := reservation.Plan.CandidateKey
			_, err := f.db.ExecContext(ctx, `UPDATE racing_add_intents SET state='confirmed',confirmed_at=? WHERE candidate_key=?`, old.Format(time.RFC3339Nano), key)
			require.NoError(t, err)
			history := models.RacingAnalysisHistory{CandidateKey: key, StartedAt: old, Samples: []models.RacingAnalysisSample{{At: old, State: "observed"}}, Peers: map[string]models.RacingPeerHistory{"192.0.2.1:6881": {IP: "192.0.2.1", Port: 6881}}}
			require.NoError(t, f.store.SaveAnalysisHistory(ctx, history))
			read, err := f.store.AnalysisHistory(ctx, key)
			require.NoError(t, err)
			require.Nil(t, read, "expired Peer endpoints must not wait for cleanup")
			var count int
			require.NoError(t, f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM racing_analysis_history WHERE candidate_key=?`, key).Scan(&count))
			require.Equal(t, 1, count, "read must not perform unbounded cleanup")
			require.NoError(t, f.store.PruneAnalysisHistory(ctx, now))
			read, err = f.store.AnalysisHistory(ctx, key)
			require.NoError(t, err)
			require.Nil(t, read, "cleanup must not resurrect an empty report")
		})
	}
}
