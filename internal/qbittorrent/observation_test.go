// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/require"
)

func TestCachedObservationsNeverRefreshAndExpire(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := &Client{Client: qbt.NewClient(qbt.Config{Host: server.URL}), instanceID: 7, isHealthy: true}
	client.updateServerState(&qbt.MainData{ServerState: qbt.ServerState{UpInfoSpeed: 10, FreeSpaceOnDisk: 900}})
	now := time.Now()
	client.preferencesCache = &qbt.AppPreferences{SavePath: "/disk-a", TempPath: "/disk-b/incomplete", TempPathEnabled: true}
	client.preferencesFetchedAt = now.Add(-time.Second)
	client.appInfoCache = &AppInfo{Version: "v5.1.2", WebAPIVersion: "2.11.3"}
	client.appInfoFetchedAt = now
	pool := &ClientPool{clients: map[int]*Client{7: client}}
	observed := pool.CachedObservations()
	require.True(t, observed[0].Fresh)
	require.Equal(t, int64(0), *observed[0].DownloadSpeed, "a known zero remains distinguishable from missing data")
	require.Equal(t, "/disk-b/incomplete", observed[0].TempPath)
	*observed[0].UploadSpeed = 999
	require.Equal(t, int64(10), *pool.CachedObservations()[0].UploadSpeed, "callers cannot mutate the shared cache")
	require.False(t, client.cachedObservation(now.Add(6*time.Second)).Fresh)
	require.False(t, client.cachedObservation(now.Add(-time.Hour)).Fresh)
	client.handleSyncManagerError(context.DeadlineExceeded)
	require.True(t, client.IsHealthy(), "slow downloader retains existing health semantics")
	require.False(t, pool.CachedObservations()[0].Fresh, "a timed-out sync must immediately invalidate freshness")
	require.Zero(t, requests.Load())
	missing := (&Client{instanceID: 8}).cachedObservation(now)
	require.False(t, missing.Fresh)
	require.Nil(t, missing.DownloadSpeed)
	require.Nil(t, missing.DefaultPathFreeBytes)
}

func TestMetadataRefreshDoesNotBlockAndCancelsWithOwner(t *testing.T) {
	started := make(chan struct{}, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &Client{Client: qbt.NewClient(qbt.Config{Host: server.URL})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client.RefreshMetadataInBackground(ctx)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata refresh did not start")
	}
	for range 10 {
		client.RefreshMetadataInBackground(ctx)
	}
	require.Equal(t, int32(1), requests.Load(), "one in-flight metadata refresh per client")
	cancel()
	require.Eventually(t, func() bool {
		client.metadataRefreshMu.Lock()
		defer client.metadataRefreshMu.Unlock()
		return !client.metadataRefreshing
	}, 10*time.Second, 5*time.Millisecond)
}

func TestSyncEventFanoutPreservesBothConsumers(t *testing.T) {
	first, second := &mockSyncEventSink{}, &mockSyncEventSink{}
	sink := SyncEventFanout{first, second}
	data := &qbt.MainData{Rid: 7}
	sink.HandleMainData(9, data)
	sink.HandleTrackerHealthUpdated(9)
	sink.HandleSyncError(9, context.DeadlineExceeded)
	for _, consumer := range []*mockSyncEventSink{first, second} {
		require.Len(t, consumer.getMainDataCalls(), 1)
		require.Same(t, data, consumer.getMainDataCalls()[0].data, "no second full torrent snapshot")
		require.Equal(t, []int{9}, consumer.getTrackerHealthUpdates())
		require.Len(t, consumer.getSyncErrorCalls(), 1)
	}
}
