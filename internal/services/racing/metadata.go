// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/racing/sources"
)

type MetadataStore interface {
	RuntimeSources(context.Context) ([]models.RacingRuntimeSource, error)
	PrivateDiscovery(context.Context, int64) ([]byte, error)
	CandidateMetadata(context.Context, string) (json.RawMessage, error)
	SaveCandidateMetadata(context.Context, string, models.RacingRuntimeSource, []byte, []byte) error
}

type metadataJob struct {
	key    string
	siteID int
	event  string
	scope  int
	rows   []models.RacingDiscovery
}

type metadataResolver struct {
	active    map[string]context.CancelFunc
	store     MetadataStore
	service   *Service
	queue     chan metadataJob
	mu        sync.Mutex
	pending   map[string]bool
	attempted map[string]time.Time
}

func newMetadataResolver(store MetadataStore, service *Service) *metadataResolver {
	return &metadataResolver{store: store, service: service, queue: make(chan metadataJob, 64), active: make(map[string]context.CancelFunc), pending: make(map[string]bool), attempted: make(map[string]time.Time)}
}

func needsMetainfo(selection RuleSelection, candidate *Candidate) bool {
	if selection.State != "waiting_metadata" {
		return false
	}
	if slices.Contains(selection.MissingFields, "verified_hash_mismatch") {
		return true
	}
	if candidate.VerifiedMetadata != nil {
		return false
	}
	for _, field := range []string{"size", "title", "reported_hash"} {
		if slices.Contains(selection.MissingFields, field) {
			return true
		}
	}
	return false
}

func (m *metadataResolver) enqueue(job metadataJob) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending[job.key] || time.Since(m.attempted[job.key]) < 30*time.Second {
		return
	}
	select {
	case m.queue <- job:
		m.pending[job.key] = true
	default:
	}
}

func (m *metadataResolver) run(ctx context.Context) {
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-m.queue:
					m.resolve(ctx, job)
					m.mu.Lock()
					delete(m.pending, job.key)
					m.attempted[job.key] = time.Now()
					for key, at := range m.attempted {
						if time.Since(at) > 5*time.Minute {
							delete(m.attempted, key)
						}
					}
					m.mu.Unlock()
				}
			}
		})
	}
	workers.Wait()
}

func (m *metadataResolver) resolve(parent context.Context, job metadataJob) {
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	m.active[job.key] = cancel
	m.mu.Unlock()
	defer func() { cancel(); m.mu.Lock(); delete(m.active, job.key); m.mu.Unlock() }()
	inputs, err := m.store.RuntimeSources(ctx)
	if err != nil {
		return
	}
	for _, row := range job.rows {
		for _, input := range inputs {
			if input.ID != row.SourceID || input.SiteID != row.SiteID || input.SiteID != job.siteID || input.SecretError {
				continue
			}
			data, err := m.store.PrivateDiscovery(ctx, row.ID)
			if err != nil {
				continue
			}
			var transport struct {
				DownloadURL string `json:"downloadUrl"`
			}
			if json.Unmarshal(data, &transport) != nil || transport.DownloadURL == "" {
				continue
			}
			fetcher := sources.Fetcher{}
			metadata, raw, err := fetcher.FetchMetainfo(ctx, sources.Request{Adapter: input.Adapter, SiteOrigin: input.SiteOrigin, Cookie: input.Cookie, Budget: m.service.discovery.requestBudget(input.SiteOrigin), RequestInterval: time.Duration(input.RequestIntervalSeconds) * time.Second}, transport.DownloadURL)
			if err != nil {
				return
			}
			public, err := json.Marshal(metadata)
			if err != nil {
				return
			}
			if m.store.SaveCandidateMetadata(ctx, job.key, input, public, raw) != nil {
				return
			}
			_ = m.service.evaluator.evaluate(ctx, job.siteID, job.event, job.scope)
			return
		}
	}
}

func (m *metadataResolver) configurationChanged() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cancel := range m.active {
		cancel()
	}
}
