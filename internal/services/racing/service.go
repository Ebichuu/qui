// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package racing owns qui's independent source configuration. Q1 observes
// and shared cached downloader state; it has no add, delete or reannounce capability.
package racing

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/autobrr/qui/internal/models"
)

type ConfigurationStore interface {
	Configuration(context.Context) (*models.RacingConfiguration, error)
}

type Status struct {
	Mode               string     `json:"mode"`
	Running            bool       `json:"running"`
	ConfigurationReady bool       `json:"configurationReady"`
	LoadedAt           *time.Time `json:"loadedAt,omitempty"`
	Sites              int        `json:"sites"`
	Sources            int        `json:"sources"`
	Groups             int        `json:"groups"`
	Rules              int        `json:"rules"`
}

type Service struct {
	metadata            *metadataResolver
	evaluator           *candidateEvaluator
	discovery           *discoveryRunner
	configuration       *models.RacingConfiguration
	observations        ObservationReader
	observationRevision atomic.Uint64
	store               ConfigurationStore
	changed             chan struct{}
	done                chan struct{}
	mu                  sync.RWMutex
	status              Status
	cancel              context.CancelFunc
}

func NewService(store ConfigurationStore) *Service {
	s := &Service{store: store, changed: make(chan struct{}, 1), status: Status{Mode: "observe_only"}}
	if discovery, ok := store.(DiscoveryStore); ok {
		s.discovery = newDiscoveryRunner(discovery)
	}
	if candidates, ok := store.(CandidateStore); ok {
		s.evaluator = newCandidateEvaluator(candidates, s)
		if s.discovery != nil {
			s.discovery.onObserved = s.evaluator.observed
		}
	}
	if metadata, ok := store.(MetadataStore); ok && s.evaluator != nil && s.discovery != nil {
		s.metadata = newMetadataResolver(metadata, s)
	}
	return s
}

// Start loads persisted configuration before reporting ready. The lifecycle is
// owned by the application context, never by an HTTP request or SSE connection.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return errors.New("racing service is already started")
	}
	if s.store == nil {
		return errors.New("racing configuration store is missing")
	}
	config, err := s.store.Configuration(ctx)
	if err != nil {
		return errors.New("load racing configuration failed")
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	s.status.Running = true
	s.setConfiguration(config)
	go s.run(runCtx)
	return nil
}

func (s *Service) setConfiguration(config *models.RacingConfiguration) {
	s.configuration = config
	now := time.Now().UTC()
	s.status.ConfigurationReady = true
	s.status.LoadedAt = &now
	s.status.Sites = len(config.Sites)
	s.status.Sources = len(config.Sources)
	s.status.Groups = len(config.Groups)
	s.status.Rules = len(config.Rules)
}

func (s *Service) run(ctx context.Context) {
	s.ConfigurationChanged()
	var evaluations sync.WaitGroup
	if s.metadata != nil {
		evaluations.Go(func() { s.metadata.run(ctx) })
	}
	if s.evaluator != nil {
		evaluations.Go(func() { s.evaluator.run(ctx) })
	}
	defer func() {
		if s.discovery != nil {
			s.discovery.stop()
		}
		evaluations.Wait()
		s.mu.Lock()
		s.status.Running = false
		s.mu.Unlock()
		close(s.done)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.changed:
			config, err := s.store.Configuration(ctx)
			if err == nil && s.discovery != nil {
				err = s.discovery.reconcile(ctx)
			}
			s.mu.Lock()
			reconfigured := err == nil && !reflect.DeepEqual(s.configuration, config)
			if err != nil {
				s.status.ConfigurationReady = false
			} else {
				s.setConfiguration(config)
			}
			s.mu.Unlock()
			if err == nil {
				if s.metadata != nil && reconfigured {
					s.metadata.configurationChanged()
				}
				if s.evaluator != nil {
					s.evaluator.configurationChanged()
				}
			}
			if err != nil && ctx.Err() == nil {
				// Retry after transient database contention, independently of the browser.
				timer := time.NewTimer(time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
					s.ConfigurationChanged()
				}
			}
		}
	}
}

// ConfigurationChanged coalesces notifications; mutations never wait for a scan.
func (s *Service) ConfigurationChanged() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status
	if status.LoadedAt != nil {
		at := *status.LoadedAt
		status.LoadedAt = &at
	}
	return status
}

func (s *Service) Stop() {
	s.mu.RLock()
	cancel, done := s.cancel, s.done
	s.mu.RUnlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}
