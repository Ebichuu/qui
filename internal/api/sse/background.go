// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sse

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// StartBackgroundSync keeps the existing per-instance sync loops alive for active
// downloaders even without browser subscribers. It adds no torrent cache or second
// qB poller. Instance configuration is reconciled independently of network syncs.
func (m *StreamManager) StartBackgroundSync() {
	m.mu.Lock()
	if m.closing.Load() || m.backgroundStarted || m.instanceDB == nil || m.syncManager == nil {
		m.mu.Unlock()
		return
	}
	m.backgroundStarted = true
	m.backgroundWG.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.backgroundWG.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
			instances, err := m.instanceDB.List(ctx)
			cancel()
			if err == nil {
				ids := make([]int, 0, len(instances))
				for _, instance := range instances {
					if instance.IsActive {
						ids = append(ids, instance.ID)
					}
				}
				m.setBackgroundInstances(ids)
			} else if m.ctx.Err() == nil {
				log.Warn().Err(err).Msg("Unable to refresh background sync instances")
			}
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (m *StreamManager) setBackgroundInstances(ids []int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing.Load() {
		return
	}
	previous := m.backgroundInstances
	m.backgroundInstances = make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		m.backgroundInstances[id] = struct{}{}
		if _, exists := m.syncLoops[id]; !exists {
			backoff := m.ensureBackoffStateLocked(id)
			m.syncLoops[id] = m.startSyncLoop(id, backoff.interval)
		}
	}
	for id := range previous {
		m.stopUnusedSyncLoopLocked(id)
	}
}

func (m *StreamManager) stopUnusedSyncLoopLocked(id int) {
	if _, watched := m.backgroundInstances[id]; watched || len(m.instanceIndex[id]) > 0 {
		return
	}
	if loop := m.syncLoops[id]; loop != nil {
		loop.cancel()
		delete(m.syncLoops, id)
	}
	delete(m.syncBackoff, id)
}
