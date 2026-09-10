// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/autobrr/qui/internal/qbittorrent"
)

type ObservationReader interface {
	CachedObservations() []qbittorrent.Observation
}

type ObservationSnapshot struct {
	StoragePools []StorageObservation      `json:"storagePools"`
	Revision     uint64                    `json:"revision"`
	Instances    []qbittorrent.Observation `json:"instances"`
}

func (s *Service) SetObservationReader(reader ObservationReader) {
	s.mu.Lock()
	s.observations = reader
	s.mu.Unlock()
}

// Observations reads the shared cache without refreshing it. Events only advance
// a revision; racing never retains a second copy of qB's full torrent state.
func (s *Service) Observations() ObservationSnapshot {
	s.mu.RLock()
	reader, config := s.observations, s.configuration
	s.mu.RUnlock()
	result := ObservationSnapshot{Revision: s.observationRevision.Load(), Instances: []qbittorrent.Observation{}, StoragePools: []StorageObservation{}}
	if reader != nil {
		result.Instances = reader.CachedObservations()
	}
	if config != nil {
		result.StoragePools = observeStorage(config, result.Instances)
	}
	return result
}
func (s *Service) HandleMainData(_ int, data *qbt.MainData) {
	if data != nil {
		s.observationRevision.Add(1)
	}
}
func (s *Service) HandleTrackerHealthUpdated(_ int) { s.observationRevision.Add(1) }
func (s *Service) HandleSyncError(_ int, err error) {
	if err != nil {
		s.observationRevision.Add(1)
	}
}
