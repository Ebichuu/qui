// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFetchFirstPageSurvivesLaterFailure(t *testing.T) {
	waiting := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			close(waiting)
			<-release
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(chdExample("", "Example Aurora Full Name")))
	}))
	defer srv.Close()
	f := Fetcher{}
	emitted := make(chan Item, 1)
	done := make(chan error, 1)
	go func() {
		done <- f.Fetch(context.Background(), Request{Adapter: "chd", Kind: "web", URL: srv.URL + "/torrents.php", PageCount: 2}, func(item Item) error { emitted <- item; return nil })
	}()
	select {
	case <-emitted:
	case <-time.After(2 * time.Second):
		t.Fatal("first page did not emit")
	}
	select {
	case <-waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("second page did not start")
	}
	close(release)
	require.ErrorIs(t, <-done, ErrRequest)
}

func TestBudgetDoesNotWaitForAnotherResponse(t *testing.T) {
	slowStarted := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(slowStarted)
			<-release
		}
		_, _ = w.Write([]byte(rssExample + `</channel></rss>`))
	}))
	defer srv.Close()
	f := Fetcher{}
	budget := &Budget{}
	slowDone := make(chan error, 1)
	input := Request{Adapter: "chd", Kind: "rss", URL: srv.URL + "/slow", PageCount: 1, Budget: budget, RequestInterval: 30 * time.Millisecond}
	go func() { slowDone <- f.Fetch(context.Background(), input, func(Item) error { return nil }) }()
	<-slowStarted
	input.URL = srv.URL + "/fast"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	fastErr := f.Fetch(ctx, input, func(Item) error { return nil })
	close(release)
	require.NoError(t, fastErr)
	require.NoError(t, <-slowDone)
}

func TestFetchCredentialBoundary(t *testing.T) {
	var externalCalls atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalCalls.Add(1)
		_, _ = w.Write([]byte(`<rss><channel/></rss>`))
	}))
	defer external.Close()
	var cookies atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "sid=private-example" {
			cookies.Add(1)
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, external.URL, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`<rss><channel/></rss>`))
	}))
	defer srv.Close()
	input := Request{Adapter: "generic-rss", Kind: "rss", URL: srv.URL + "/feed", SiteOrigin: srv.URL, Cookie: "sid=private-example", PageCount: 1}
	f := Fetcher{}
	require.NoError(t, f.Fetch(context.Background(), input, func(Item) error { return nil }))
	require.Equal(t, int32(1), cookies.Load())
	input.SiteOrigin = external.URL
	require.NoError(t, f.Fetch(context.Background(), input, func(Item) error { return nil }))
	require.Equal(t, int32(1), cookies.Load())
	input.URL = srv.URL + "/redirect?passkey=private-example"
	err := f.Fetch(context.Background(), input, func(Item) error { return nil })
	require.ErrorIs(t, err, ErrRequest)
	require.NotContains(t, err.Error(), "private-example")
	require.Zero(t, externalCalls.Load())
}

func TestFetchResponseLimitsAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			panic(http.ErrAbortHandler)
		}
		_, _ = w.Write([]byte(`<rss><channel>` + strings.Repeat(" ", 2048) + `</channel></rss>`))
	}))
	defer srv.Close()
	f := Fetcher{MaxBytes: 100, Timeout: 20 * time.Millisecond}
	input := Request{Adapter: "generic-rss", Kind: "rss", URL: srv.URL, PageCount: 1}
	require.ErrorIs(t, f.Fetch(context.Background(), input, func(Item) error { return nil }), ErrResponseLimit)
	input.URL += "/slow"
	require.ErrorIs(t, f.Fetch(context.Background(), input, func(Item) error { return nil }), ErrRequest)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, f.Fetch(ctx, input, func(Item) error { return nil }), context.Canceled)
}
