// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package qbittorrent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/require"
)

func TestSingleAttemptAddDoesNotReplayAndSharesAuthentication(t *testing.T) {
	for _, mode := range []string{"disconnect", "redirect", "success"} {
		t.Run(mode, func(t *testing.T) {
			var adds, unexpected atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				cookie, err := r.Cookie("SID")
				if r.URL.Path != "/api/v2/torrents/add" || err != nil || cookie.Value != "synthetic-session" {
					unexpected.Add(1)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				adds.Add(1)
				switch mode {
				case "disconnect":
					connection, _, err := http.NewResponseController(w).Hijack()
					if err == nil {
						_ = connection.Close()
					}
				case "redirect":
					w.Header().Set("Location", "/api/v2/torrents/add")
					w.WriteHeader(http.StatusTemporaryRedirect)
				case "success":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"success_count":1}`))
				}
			}))
			defer server.Close()
			config := qbt.Config{Host: server.URL}
			shared := qbt.NewClient(config)
			origin, err := url.Parse(server.URL)
			require.NoError(t, err)
			shared.GetHTTPClient().Jar.SetCookies(origin, []*http.Cookie{{Name: "SID", Value: "synthetic-session"}})
			client := newSingleAttemptClient(config, shared)
			require.Same(t, shared.GetHTTPClient().Jar, client.GetHTTPClient().Jar)
			require.Same(t, shared.GetHTTPClient().Transport, client.GetHTTPClient().Transport)
			_, err = client.AddTorrentFromMemoryCtx(t.Context(), []byte("synthetic metainfo"), nil)
			if mode == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, int32(1), adds.Load())
			require.Zero(t, unexpected.Load())
		})
	}
}

func TestSingleAttemptDeleteDoesNotReplay(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/delete" {
			t.Errorf("unexpected delete path: %s", r.URL.Path)
		}
		calls.Add(1)
		connection, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	defer server.Close()
	config := qbt.Config{Host: server.URL}
	client := newSingleAttemptClient(config, qbt.NewClient(config))
	require.Error(t, client.DeleteTorrentsCtx(t.Context(), []string{"synthetic-delete"}, true))
	require.Equal(t, int32(1), calls.Load())
}
