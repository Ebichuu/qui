// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package reannounce

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryConfirmsExactTrackers(t *testing.T) {
	failed := func(url string) qbt.TorrentTracker {
		return qbt.TorrentTracker{Url: url, Status: qbt.TrackerStatusNotWorking}
	}
	healthy := func(url string) qbt.TorrentTracker { return qbt.TorrentTracker{Url: url, Status: qbt.TrackerStatusOK} }
	a, b := "https://a.example.invalid/announce", "https://b.example.invalid/announce"
	tests := []struct {
		name  string
		after []qbt.TorrentTracker
		want  error
	}{
		{"all accepted", []qbt.TorrentTracker{healthy(a), healthy(b)}, nil},
		{"retry exhausted", []qbt.TorrentTracker{failed(a), failed(b)}, errTrackerUnconfirmed},
		{"partial acceptance", []qbt.TorrentTracker{healthy(a), failed(b)}, errTrackerPartial},
		{"different tracker cannot confirm", []qbt.TorrentTracker{healthy(a), healthy("https://c.example.invalid/announce")}, errTrackerChanged},
		{"missing inventory", nil, errTrackerChanged},
		{"unregistered despite OK", []qbt.TorrentTracker{{Url: a, Status: qbt.TrackerStatusOK, Message: "torrent not registered"}, failed(b)}, errTrackerUnconfirmed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var posts atomic.Int32
			initial := []qbt.TorrentTracker{failed(a), failed(b)}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/trackers":
					rows := initial
					if posts.Load() > 0 {
						rows = tt.after
					}
					assert.NoError(t, json.NewEncoder(w).Encode(rows))
				case "/api/v2/torrents/reannounce":
					posts.Add(1)
					w.WriteHeader(http.StatusOK)
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := qbt.NewClient(qbt.Config{Host: server.URL})
			err := retryReannounce(t.Context(), client, "synthetic", initial, time.Millisecond, 1)
			require.ErrorIs(t, err, tt.want)
			require.EqualValues(t, 1, posts.Load())
		})
	}
}

func TestRetryWaitsWithoutRequestsAndCancels(t *testing.T) {
	var posts atomic.Int32
	initial := []qbt.TorrentTracker{{Url: "https://a.example.invalid/announce", Status: qbt.TrackerStatusUpdating}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/reannounce" {
			posts.Add(1)
		}
		assert.NoError(t, json.NewEncoder(w).Encode(initial))
	}))
	defer server.Close()
	client := qbt.NewClient(qbt.Config{Host: server.URL})
	require.ErrorIs(t, retryReannounce(t.Context(), client, "synthetic", initial, time.Millisecond, 2), errTrackerUnconfirmed)
	require.Zero(t, posts.Load())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, retryReannounce(ctx, client, "synthetic", initial, time.Hour, 2), context.Canceled)
}
