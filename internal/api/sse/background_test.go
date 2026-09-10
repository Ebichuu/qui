// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackgroundSyncSharesLoopWithBrowserAndSurvivesDisconnect(t *testing.T) {
	m := NewStreamManager(nil, nil, nil)
	t.Cleanup(func() { require.NoError(t, m.Shutdown(context.Background())) })
	m.setBackgroundInstances([]int{7, 7, 9})
	require.Len(t, m.syncLoops, 2)
	require.Empty(t, m.groups, "background sync does not materialize torrent views")
	require.Empty(t, m.heartbeatLoops)
	loop := m.syncLoops[7]
	id, err := m.registerSubscription(StreamOptions{InstanceID: 7}, "browser")
	require.NoError(t, err)
	require.Same(t, loop, m.syncLoops[7], "one shared loop per instance")
	m.Unregister(id)
	require.Same(t, loop, m.syncLoops[7], "closing the last page must not stop background sync")
	require.Empty(t, m.heartbeatLoops, "browser heartbeat stops on disconnect")
	m.setBackgroundInstances([]int{9})
	require.NotContains(t, m.syncLoops, 7)
	require.NotContains(t, m.syncBackoff, 7)
	require.Len(t, m.syncLoops, 1)
}

func TestBackgroundSyncRemovalPreservesActiveBrowser(t *testing.T) {
	m := NewStreamManager(nil, nil, nil)
	t.Cleanup(func() { require.NoError(t, m.Shutdown(context.Background())) })
	id, err := m.registerSubscription(StreamOptions{InstanceIDs: []int{7, 9}}, "browser")
	require.NoError(t, err)
	loop := m.syncLoops[7]
	m.setBackgroundInstances([]int{7})
	m.setBackgroundInstances(nil)
	require.Same(t, loop, m.syncLoops[7])
	m.Unregister(id)
	require.Empty(t, m.syncLoops)
	require.NoError(t, m.Shutdown(context.Background()))
	m.setBackgroundInstances([]int{7})
	require.Empty(t, m.syncLoops, "shutdown prevents new background workers")
}
