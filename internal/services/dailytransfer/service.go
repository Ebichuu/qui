// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dailytransfer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/qbittorrent"
)

const defaultSampleInterval = time.Minute

// Service periodically samples qBittorrent's all-time counters and persists
// their deltas as local calendar-day transfer totals.
type Service struct {
	instanceStore *models.InstanceStore
	clientPool    *qbittorrent.ClientPool
	store         *models.DailyTransferStatsStore
	location      *time.Location
	interval      time.Duration
}

func NewService(instanceStore *models.InstanceStore, clientPool *qbittorrent.ClientPool, store *models.DailyTransferStatsStore, location *time.Location) *Service {
	if location == nil {
		location = time.Local
	}
	return &Service{
		instanceStore: instanceStore,
		clientPool:    clientPool,
		store:         store,
		location:      location,
		interval:      defaultSampleInterval,
	}
}

func (s *Service) Start(ctx context.Context) {
	if s == nil || s.instanceStore == nil || s.clientPool == nil || s.store == nil {
		return
	}
	go s.run(ctx)
}

func (s *Service) run(ctx context.Context) {
	s.sampleAll(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sampleAll(ctx)
		}
	}
}

func (s *Service) sampleAll(ctx context.Context) {
	instances, err := s.instanceStore.List(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("daily transfer stats: failed to list instances")
		return
	}
	for _, instance := range instances {
		if !instance.IsActive {
			continue
		}
		sampleCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, err := s.SampleInstance(sampleCtx, instance.ID)
		cancel()
		if err != nil {
			log.Debug().Err(err).Int("instanceID", instance.ID).Msg("daily transfer stats: sample failed")
		}
	}
}

func (s *Service) SampleInstance(ctx context.Context, instanceID int) (*models.DailyTransferStats, error) {
	if s == nil || s.clientPool == nil || s.store == nil {
		return nil, errors.New("daily transfer stats service is not configured")
	}

	client, err := s.clientPool.GetClient(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("connect to qBittorrent: %w", err)
	}
	if syncManager := client.GetSyncManager(); syncManager != nil {
		if err := syncManager.Sync(ctx); err != nil && client.GetCachedServerState() == nil {
			return nil, fmt.Errorf("sync qBittorrent server state: %w", err)
		}
	}

	state := client.GetCachedServerState()
	if state == nil {
		return nil, errors.New("qBittorrent did not return all-time transfer counters")
	}

	now := time.Now().In(s.location)
	return s.store.Record(ctx, instanceID, now, state.AlltimeDl, state.AlltimeUl)
}

// Current samples live counters when possible and falls back to today's last
// persisted value while an instance is temporarily unreachable.
func (s *Service) Current(ctx context.Context, instanceID int) (*models.DailyTransferStats, error) {
	stats, sampleErr := s.SampleInstance(ctx, instanceID)
	if sampleErr == nil {
		return stats, nil
	}

	day := time.Now().In(s.location).Format("2006-01-02")
	stats, err := s.store.Get(ctx, instanceID, day)
	if err == nil {
		return stats, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load persisted daily transfer stats: %w", err)
	}
	return nil, sampleErr
}
