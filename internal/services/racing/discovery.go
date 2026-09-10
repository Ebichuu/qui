// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/racing/sources"
)

type DiscoveryStore interface {
	RuntimeSources(context.Context) ([]models.RacingRuntimeSource, error)
	SourceBaseline(context.Context, int, int) (time.Time, error)
	SaveDiscovery(context.Context, models.RacingRuntimeSource, string, []byte, []byte, bool) error
	CompleteSourceScan(context.Context, models.RacingRuntimeSource) error
}

type SourceStatus struct {
	SourceID      int        `json:"sourceId"`
	InFlight      bool       `json:"inFlight"`
	LastStartedAt *time.Time `json:"lastStartedAt,omitempty"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
	LastItemCount int        `json:"lastItemCount"`
}

type sourceWorker struct {
	source          models.RacingRuntimeSource
	requestInterval int
	cancel          context.CancelFunc
}

type discoveryRunner struct {
	store    DiscoveryStore
	fetcher  sources.Fetcher
	workers  map[int]sourceWorker
	budgets  map[string]*sources.Budget
	wg       sync.WaitGroup
	mu       sync.RWMutex
	statuses map[int]SourceStatus
}

func newDiscoveryRunner(store DiscoveryStore) *discoveryRunner {
	return &discoveryRunner{store: store, workers: make(map[int]sourceWorker), budgets: make(map[string]*sources.Budget), statuses: make(map[int]SourceStatus)}
}

// Called only by the service lifecycle goroutine. In-flight results also carry
// a database revision fence, so a canceled worker cannot publish old settings.
func (d *discoveryRunner) reconcile(ctx context.Context) error {
	inputs, err := d.store.RuntimeSources(ctx)
	if err != nil {
		return err
	}
	active := make(map[int]models.RacingRuntimeSource, len(inputs))
	intervals := make(map[string]int)
	for _, input := range inputs {
		active[input.ID] = input
		key := strings.ToLower(input.SiteOrigin)
		intervals[key] = max(intervals[key], input.RequestIntervalSeconds)
	}
	for id, worker := range d.workers {
		next, ok := active[id]
		if !ok || next.UpdatedAt != worker.source.UpdatedAt || next.SiteRevision != worker.source.SiteRevision || intervals[strings.ToLower(next.SiteOrigin)] != worker.requestInterval {
			worker.cancel()
			delete(d.workers, id)
			d.mu.Lock()
			delete(d.statuses, id)
			d.mu.Unlock()
		}
	}
	for key := range d.budgets {
		if _, ok := intervals[key]; !ok {
			delete(d.budgets, key)
		}
	}
	for _, input := range inputs {
		if _, ok := d.workers[input.ID]; ok {
			continue
		}
		key := strings.ToLower(input.SiteOrigin)
		budget := d.budgets[key]
		if budget == nil {
			budget = &sources.Budget{}
			d.budgets[key] = budget
		}
		workerCtx, cancel := context.WithCancel(ctx)
		d.workers[input.ID] = sourceWorker{source: input, cancel: cancel, requestInterval: intervals[key]}
		d.wg.Go(func() { d.observe(workerCtx, input, budget, time.Duration(intervals[key])*time.Second) })
	}
	return nil
}

func (d *discoveryRunner) stop() {
	for _, worker := range d.workers {
		worker.cancel()
	}
	d.wg.Wait()
}

func (d *discoveryRunner) update(ctx context.Context, status SourceStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Old workers cannot overwrite a replacement's displayed status after cancel.
	if ctx.Err() == nil {
		d.statuses[status.SourceID] = status
	}
}

func (d *discoveryRunner) observe(ctx context.Context, input models.RacingRuntimeSource, budget *sources.Budget, requestInterval time.Duration) {
	status := SourceStatus{SourceID: input.ID}
	failures := 0
	for ctx.Err() == nil {
		now := time.Now().UTC()
		status.LastStartedAt = &now
		status.InFlight = true
		status.NextAttemptAt = nil
		status.LastItemCount = 0
		d.update(ctx, status)
		_, err := sources.Pages(input.Adapter, input.Kind, input.URL, input.PageCount)
		if input.SecretError {
			err = errors.New("source secret unavailable")
		}
		var baseline time.Time
		if err == nil {
			baseline, err = d.store.SourceBaseline(ctx, input.ID, input.InitialLookbackSeconds)
		}
		if err == nil {
			err = d.fetcher.Fetch(ctx, sources.Request{Adapter: input.Adapter, Kind: input.Kind, URL: input.URL, SiteOrigin: input.SiteOrigin, Cookie: input.Cookie, PageCount: input.PageCount, Budget: budget, RequestInterval: requestInterval}, func(item sources.Item) error {
				public, err := json.Marshal(item.PublicItem)
				if err != nil {
					return err
				}
				private, err := json.Marshal(struct {
					DownloadURL string `json:"downloadUrl"`
					DetailsURL  string `json:"detailsUrl"`
				}{item.DownloadURL, item.DetailsURL})
				if err != nil {
					return err
				}
				eligible := item.PublishedAt != nil && !item.PublishedAt.Before(baseline)
				if err := d.store.SaveDiscovery(ctx, input, item.EventKey, public, private, eligible); err != nil {
					return err
				}
				status.LastItemCount++
				return nil
			})
		}
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = d.store.CompleteSourceScan(ctx, input)
		}
		status.InFlight = false
		delay := time.Duration(input.IntervalSeconds) * time.Second
		if err != nil {
			failures = min(failures+1, 6)
			delay = max(delay, time.Duration(1<<failures)*time.Second)
			status.LastError = sourceErrorCode(err)
		} else {
			failures = 0
			status.LastError = ""
			at := time.Now().UTC()
			status.LastSuccessAt = &at
		}
		next := time.Now().UTC().Add(delay)
		status.NextAttemptAt = &next
		d.update(ctx, status)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func sourceErrorCode(err error) string {
	switch {
	case errors.Is(err, sources.ErrAuthentication):
		return "authentication_required"
	case errors.Is(err, sources.ErrUnsupported):
		return "unsupported_adapter"
	case errors.Is(err, sources.ErrInvalidPage):
		return "invalid_listing"
	case errors.Is(err, sources.ErrResponseLimit):
		return "response_too_large"
	case errors.Is(err, sources.ErrRequest):
		return "request_failed"
	default:
		return "persistence_unavailable"
	}
}

func (s *Service) SourceStatuses() []SourceStatus {
	result := []SourceStatus{}
	if s.discovery == nil {
		return result
	}
	s.discovery.mu.RLock()
	defer s.discovery.mu.RUnlock()
	for _, status := range s.discovery.statuses {
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SourceID < result[j].SourceID })
	return result
}
