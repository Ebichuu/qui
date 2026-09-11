// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package qbittorrent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/require"
)

func TestExecutionObservationUsesOneSharedSnapshotWithoutRefresh(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"rid":1,"full_update":true,"torrents":{"a":{"hash":"a","size":100,"total_size":100,"completed":20,"downloaded":900,"amount_left":80,"save_path":"/data","state":"pausedDL"}},"categories":{},"server_state":{"free_space_on_disk":400,"dl_info_speed":7,"up_info_speed":8}}`)
	}))
	defer server.Close()
	raw := qbt.NewClient(qbt.Config{Host: server.URL})
	client := &Client{Client: raw, instanceID: 1, isHealthy: true}
	client.syncManager = raw.NewSyncManager(qbt.DefaultSyncOptions())
	client.preferencesCache = &qbt.AppPreferences{SavePath: "/data"}
	client.preferencesFetchedAt = time.Now().Add(-time.Second)
	require.NoError(t, client.syncManager.Sync(t.Context()))
	before := calls.Load()
	// The separate UI cache deliberately contains another generation's free
	// space; execution must only use the atomic MainData copy.
	client.updateServerState(&qbt.MainData{ServerState: qbt.ServerState{FreeSpaceOnDisk: 9999}})
	projection := client.cachedExecutionObservation(time.Now())
	require.True(t, projection.Fresh)
	require.Equal(t, int64(400), *projection.DefaultPathFreeBytes)
	require.Len(t, projection.Torrents, 1)
	require.Equal(t, int64(80), projection.Torrents[0].Remaining, "traffic bytes are not completed payload bytes")
	require.Equal(t, before, calls.Load(), "execution observations must not poll qB")
	projection.Torrents[0].Remaining = 0
	require.Equal(t, int64(80), client.cachedExecutionObservation(time.Now()).Torrents[0].Remaining)
	require.False(t, client.cachedExecutionObservation(time.Now().Add(6*time.Second)).Fresh)
}
