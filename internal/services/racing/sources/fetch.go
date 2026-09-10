// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sources

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

var ErrRequest = errors.New("source request failed")
var ErrResponseLimit = errors.New("source response exceeds size limit")

// Budget serializes request starts, not responses. A slow RSS response cannot
// occupy the site's entire request budget and hold a web response behind it.
type Budget struct {
	mu   sync.Mutex
	next time.Time
}

func (b *Budget) Wait(ctx context.Context, interval time.Duration) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		delay := time.Until(b.next)
		if delay <= 0 {
			b.next = time.Now().Add(interval)
			b.mu.Unlock()
			return nil
		}
		b.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type Fetcher struct {
	Client   *http.Client
	Timeout  time.Duration
	MaxBytes int64
}

type Request struct {
	Adapter         string
	Kind            string
	URL             string
	SiteOrigin      string
	Cookie          string
	PageCount       int
	RequestInterval time.Duration
	Budget          *Budget
}

func sameOrigin(a, b string) bool {
	left, e1 := url.Parse(a)
	right, e2 := url.Parse(b)
	return e1 == nil && e2 == nil && left.Scheme == right.Scheme && left.Host == right.Host
}

// Fetch delivers each item before moving on to later pages. An error describes
// an incomplete scan; items already accepted by emit must remain committed.
// It never fetches a torrent file or follows a candidate's download URL.
func (f *Fetcher) Fetch(ctx context.Context, input Request, emit Emit) error {
	pages, err := Pages(input.Adapter, input.Kind, input.URL, input.PageCount)
	if err != nil {
		return err
	}
	client := http.Client{}
	if f.Client != nil {
		client = *f.Client
	}
	// Never forward credentials through redirects, including a redirect to login
	// on a different host. URLs in errors are deliberately not returned.
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !sameOrigin(via[0].URL.String(), req.URL.String()) {
			return ErrRequest
		}
		return nil
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	limit := f.MaxBytes
	if limit <= 0 {
		limit = 8 << 20
	}
	for _, page := range pages {
		if input.Budget != nil {
			if err := input.Budget.Wait(ctx, input.RequestInterval); err != nil {
				return err
			}
		}
		if err := f.page(ctx, &client, input, page, timeout, limit, emit); err != nil {
			return err
		}
	}
	return nil
}

func (f *Fetcher) page(parent context.Context, client *http.Client, input Request, address string, timeout time.Duration, limit int64, emit Emit) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return ErrRequest
	}
	req.Header.Set("User-Agent", "qui-source-observer")
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/html;q=0.9")
	if input.Cookie != "" && sameOrigin(address, input.SiteOrigin) {
		req.Header.Set("Cookie", input.Cookie)
	}
	response, err := client.Do(req)
	if err != nil {
		if parent.Err() != nil {
			return parent.Err()
		}
		return ErrRequest
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return ErrAuthentication
	}
	if response.StatusCode != http.StatusOK {
		return ErrRequest
	}
	reader := &limitedResponse{reader: response.Body, remaining: limit}
	err = Parse(ctx, input.Adapter, input.Kind, address, reader, emit)
	if reader.exceeded {
		return ErrResponseLimit
	}
	if ctx.Err() != nil {
		if parent.Err() != nil {
			return parent.Err()
		}
		return ErrRequest
	}
	return err
}

type limitedResponse struct {
	reader    io.Reader
	remaining int64
	exceeded  bool
}

func (r *limitedResponse) Read(p []byte) (int, error) {
	if int64(len(p)) > r.remaining+1 {
		p = p[:r.remaining+1]
	}
	n, err := r.reader.Read(p)
	if int64(n) > r.remaining {
		r.exceeded = true
		return 0, ErrResponseLimit
	}
	r.remaining -= int64(n)
	return n, err
}
