// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package reannounce

import (
	"bytes"
	"testing"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestTrackerConstraintsRequireEveryAccountAndRetainOriginalInterval(t *testing.T) {
	db := testdb.NewMigratedSQLite(t, "tracker-constraints")
	instances, err := models.NewInstanceStore(db, bytes.Repeat([]byte{9}, 32))
	require.NoError(t, err)
	instance, err := instances.Create(t.Context(), "Synthetic constraints", "http://127.0.0.1:1", "test", "test", nil, nil, false, nil)
	require.NoError(t, err)
	store := models.NewInstanceReannounceStore(db)
	service := &Service{settingsStore: store}
	torrent := qbt.Torrent{Hash: "abc", AddedOn: 100}
	a := qbt.TorrentTracker{Url: "https://a.example.invalid/announce", Status: qbt.TrackerStatusOK}
	b := qbt.TorrentTracker{Url: "https://b.example.invalid/announce", Status: qbt.TrackerStatusOK}
	policies := []models.ReannounceTrackerPolicy{
		{TrackerKey: trackerDigest(a.Url), TrackerHost: "a.example.invalid", IntervalSeconds: 1, DeleteProtection: "reported_working"},
		{TrackerKey: trackerDigest(b.Url), TrackerHost: "b.example.invalid", IntervalSeconds: 1, DeleteProtection: "reported_working"},
	}
	check := func(trackers []qbt.TorrentTracker, policies []models.ReannounceTrackerPolicy) TrackerConstraintResult {
		t.Helper()
		result, err := service.trackerConstraints(t.Context(), instance.ID, torrent, trackers, policies, false)
		require.NoError(t, err)
		return result
	}
	require.True(t, check([]qbt.TorrentTracker{a, b}, policies).DeleteAllowed)
	result := check([]qbt.TorrentTracker{a, b}, policies[:1])
	require.False(t, result.DeleteAllowed)
	require.False(t, result.ReannounceAllowed)
	result = check([]qbt.TorrentTracker{a}, policies)
	require.Equal(t, "tracker_removed", result.Reason)
	altered := a
	altered.Url += "?account=another-synthetic-account"
	require.False(t, check([]qbt.TorrentTracker{altered, b}, policies).DeleteAllowed)
	policies[1].DeleteProtection = "accounted"
	require.False(t, check([]qbt.TorrentTracker{a, b}, policies).DeleteAllowed)
	policies[1].DeleteProtection = "reported_working"
	for _, status := range []qbt.TrackerStatus{qbt.TrackerStatusUpdating, qbt.TrackerStatusNotContacted, qbt.TrackerStatusDisabled} {
		altered = a
		altered.Status = status
		result = check([]qbt.TorrentTracker{altered, b}, policies)
		require.False(t, result.DeleteAllowed)
		require.False(t, result.ReannounceAllowed)
	}
	sentAt := time.Now().Add(-2 * time.Second)
	allowed, err := store.BeginReannounce(t.Context(), instance.ID, torrent.Hash, sentAt, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
	result = check([]qbt.TorrentTracker{a, b}, policies)
	require.False(t, result.DeleteAllowed)
	require.False(t, result.ReannounceAllowed)
	require.True(t, result.NotBefore.Equal(sentAt.Add(time.Minute)))
}
