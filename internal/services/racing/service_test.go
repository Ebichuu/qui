// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

type testConfigurationStore struct {
	mu    sync.Mutex
	sites int
	fail  bool
	calls int
}

func (s *testConfigurationStore) Configuration(ctx context.Context) (*models.RacingConfiguration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.fail {
		return nil, errors.New("synthetic database error")
	}
	return &models.RacingConfiguration{Sites: make([]models.RacingSite, s.sites)}, ctx.Err()
}

func TestServiceLifecycleAndConfigurationRefresh(t *testing.T) {
	store := &testConfigurationStore{}
	service := NewService(store)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	require.NoError(t, service.Start(ctx))
	t.Cleanup(service.Stop)
	require.True(t, service.Status().Running)
	require.True(t, service.Status().ConfigurationReady)
	require.Equal(t, "observe_only", service.Status().Mode)
	require.Error(t, service.Start(ctx))
	store.mu.Lock()
	store.sites = 2
	store.mu.Unlock()
	service.ConfigurationChanged()
	require.Eventually(t, func() bool { return service.Status().Sites == 2 }, time.Second, time.Millisecond)
	// Unrelated HTTP/request cancellation cannot affect the application context.
	requestCtx, requestCancel := context.WithCancel(ctx)
	requestCancel()
	require.ErrorIs(t, requestCtx.Err(), context.Canceled)
	require.True(t, service.Status().Running)
	store.mu.Lock()
	store.fail = true
	store.mu.Unlock()
	service.ConfigurationChanged()
	require.Eventually(t, func() bool { return !service.Status().ConfigurationReady }, time.Second, time.Millisecond)
	store.mu.Lock()
	store.fail = false
	store.sites = 3
	store.mu.Unlock()
	// A transient database failure recovers without a browser-triggered refresh.
	require.Eventually(t, func() bool { return service.Status().ConfigurationReady && service.Status().Sites == 3 }, 3*time.Second, 10*time.Millisecond)
	cancel()
	service.Stop()
	require.False(t, service.Status().Running)
	restarted := NewService(store)
	require.NoError(t, restarted.Start(t.Context()))
	defer restarted.Stop()
	require.Equal(t, 3, restarted.Status().Sites)
}
