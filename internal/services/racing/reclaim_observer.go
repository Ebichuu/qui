// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/automations"
)

type ReclaimCandidateReader interface {
	ObserveReclaimCandidates(context.Context, int) (*automations.ReclaimCandidates, error)
	ReclaimPhysicalBytes(context.Context, int, automations.ReclaimCandidate, []int) (*int64, error)
}

func (s *Service) SetReclaimCandidateReader(reader ReclaimCandidateReader) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reclaimReader = reader
}

// One bounded assessment worker runs independently of addition/confirmation
// slots. Slow file or condition dependencies cannot hold the reception loop.
func (e *executionRunner) launchReclaimAssessment(ctx context.Context, record models.RacingCandidateRecord, candidate Candidate, selection RuleSelection, config *models.RacingConfiguration, reader ExecutionReader) {
	e.service.mu.RLock()
	observer := e.service.reclaimReader
	e.service.mu.RUnlock()
	if observer == nil {
		return
	}
	key := "reclaim:" + record.Key
	e.mu.Lock()
	if e.reclaimBusy || time.Since(e.attempted[key]) < 20*time.Second {
		e.mu.Unlock()
		return
	}
	e.reclaimBusy = true
	e.attempted[key] = time.Now()
	e.mu.Unlock()
	e.workers.Go(func() {
		defer func() {
			e.mu.Lock()
			e.reclaimBusy = false
			for key, at := range e.attempted {
				if time.Since(at) > 5*time.Minute {
					delete(e.attempted, key)
				}
			}
			e.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		intents, err := e.allIntents(ctx)
		if err != nil {
			return
		}
		observations := reader.CachedExecutionObservations()
		budgets := executionBudgets(config, observations, intents, time.Now())
		targets := receptionTargetsWithCapacity(*selection.Rule, config, config.ExecutionPolicies, observations, intents, budgets, candidate.VerifiedMetadata.SizeBytes, time.Now(), true)
		targets = avoidDuplicateParticipants(targets, observations, candidate.VerifiedMetadata.HashV1, candidate.VerifiedMetadata.HashV2)
		settings, err := e.store.ReclaimConfiguration(ctx)
		if err != nil || settings.Revision != config.Revision {
			return
		}
		for _, target := range targets {
			if ctx.Err() != nil || target.available >= candidate.VerifiedMetadata.SizeBytes || len(target.pools) != 1 {
				continue
			}
			var policy *models.RacingReclaimPolicy
			for _, item := range settings.Effective {
				if item.InstanceID == target.instance.InstanceID {
					policy = item.Policy
					break
				}
			}
			if policy == nil || !policy.Enabled {
				continue
			}
			candidates, err := observer.ObserveReclaimCandidates(ctx, target.instance.InstanceID)
			if err != nil || candidates.Revision != settings.Revision {
				continue
			}
			now := time.Now()
			if selection.Deadline == nil || !now.Before(*selection.Deadline) {
				return
			}
			poolInstances := []int{}
			for _, mapping := range config.PathMappings {
				if mapping.StoragePoolID == target.pools[0] && !slices.Contains(poolInstances, mapping.InstanceID) {
					poolInstances = append(poolInstances, mapping.InstanceID)
				}
			}
			evidence := []ReclaimEvidence{}
			for _, item := range candidates.Candidates {
				pool, known := ResolveStoragePool(config.PathMappings, target.instance.InstanceID, item.SavePath)
				if !known {
					continue
				}
				// Logical size is never promoted into physical release evidence.
				entry := ReclaimEvidence{Hash: item.Hash, AddedOn: item.AddedOn, InstanceID: target.instance.InstanceID, PoolID: pool, ObservedAt: item.ObservedAt, LowEfficiencyDuration: time.Duration(item.LowEfficiencySeconds) * time.Second}
				if item.RecentUploadBytes != nil && item.UploadWindowSeconds == policy.RecentUploadWindowSeconds {
					entry.UploadWindowCovered = true
					entry.RecentUploadBytes = *item.RecentUploadBytes
				}
				if pool == target.pools[0] {
					if bytes, err := observer.ReclaimPhysicalBytes(ctx, target.instance.InstanceID, item, poolInstances); err == nil && bytes != nil {
						entry.CapacityKnown = true
						entry.PhysicalBytes = *bytes
					}
				}
				evidence = append(evidence, entry)
			}
			now = time.Now()
			if !now.Before(*selection.Deadline) {
				return
			}
			result := assessReclaim(target.instance.InstanceID, target.pools[0], candidate.VerifiedMetadata.SizeBytes, target.available, *policy, *policy, ReclaimSpent{}, evidence, now)
			if result.State == "assessed" {
				result.State = "awaiting_site_protection"
			}
			raw, err := json.Marshal(result)
			if err != nil {
				continue
			}
			_ = e.store.SaveReclaimAssessment(ctx, models.RacingReclaimAssessment{CandidateKey: record.Key, InstanceID: target.instance.InstanceID, ConfigurationRevision: settings.Revision, Assessment: raw, ObservedAt: now.UTC().Format(time.RFC3339Nano)})
		}
	})
}
