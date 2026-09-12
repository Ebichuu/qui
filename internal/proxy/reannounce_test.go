// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/qbittorrent"
)

type testReannounceDispatcher struct {
	err      error
	instance int
	hashes   []string
}

func (d *testReannounceDispatcher) DispatchReannounce(_ context.Context, id int, hashes []string) error {
	d.instance, d.hashes = id, hashes
	return d.err
}

func TestProxyReannounceNeverFallsBackAfterDispatch(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "scheduled", true: "blocked"}[failed], func(t *testing.T) {
			dispatcher := &testReannounceDispatcher{}
			if failed {
				dispatcher.err = errors.New("storage unavailable")
			}
			sm := &qbittorrent.SyncManager{}
			sm.SetReannounceDispatcher(dispatcher)
			// No client or reverse proxy is available: any fallback would fail.
			h := &Handler{syncManager: sm}
			r := httptest.NewRequest(http.MethodPost, "/api/v2/torrents/reannounce", strings.NewReader("hashes=abc%7Cabc%7Cdef"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r = r.WithContext(context.WithValue(r.Context(), InstanceIDContextKey, 42))
			w := httptest.NewRecorder()
			h.handleReannounce(w, r)
			if failed {
				require.Equal(t, http.StatusServiceUnavailable, w.Code)
			} else {
				require.Equal(t, http.StatusOK, w.Code)
			}
			require.Equal(t, 42, dispatcher.instance)
			require.Equal(t, []string{"ABC", "DEF"}, dispatcher.hashes)
		})
	}
}
