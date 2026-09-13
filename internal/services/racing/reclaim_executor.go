// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"context"
	"strings"
	"time"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/automations"
	"github.com/autobrr/qui/pkg/fileallocation"
)

type ReclaimExecutionClient interface {
	ReclaimDelete(context.Context, models.RacingReclaimPlan, fileallocation.ReleaseBaseline, string, string) error
	ReconcileAutomaticDeletes(context.Context, int) error
}

type ReclaimFilesystemReader interface {
	ReclaimReleaseBaseline(context.Context, int, automations.ReclaimCandidate, []int, string) (*fileallocation.ReleaseBaseline, error)
	ReclaimReleaseSpace(context.Context, int, fileallocation.ReleaseBaseline) (fileallocation.Space, bool, error)
}

func (e *executionRunner) executeReclaim(ctx context.Context, plan models.RacingReclaimPlan, candidate Candidate, selection RuleSelection, config *models.RacingConfiguration, reader ExecutionReader, observer ReclaimCandidateReader, poolInstances []int) {
	e.service.mu.RLock()
	client, ok := e.service.executionClient.(ReclaimExecutionClient)
	e.service.mu.RUnlock()
	files, supported := observer.(ReclaimFilesystemReader)
	if !ok || !supported || len(plan.Items) == 0 {
		return
	}
	authorized := false
	for _, policy := range config.ExecutionPolicies {
		if policy.InstanceID == plan.InstanceID && policy.Enabled && policy.ReclaimEnabled {
			authorized = true
		}
	}
	if !authorized {
		return
	}
	latest, err := observer.ObserveReclaimCandidates(ctx, plan.InstanceID)
	if err != nil || latest.Revision != plan.ConfigurationRevision {
		return
	}
	// The entire combination must still qualify; deleting its first item
	// cannot rescue a plan whose remaining candidates no longer qualify.
	for _, expected := range plan.Items {
		valid := false
		for _, current := range latest.Candidates {
			if strings.EqualFold(current.Hash, expected.Hash) && current.AddedOn == expected.AddedOn && current.RecentUploadBytes != nil && *current.RecentUploadBytes <= expected.RecentUploadBytes && current.UploadWindowSeconds == plan.Frozen.RecentUploadWindowSeconds {
				valid = true
				break
			}
		}
		if !valid {
			return
		}
	}
	first := plan.Items[0]
	for _, item := range latest.Candidates {
		if !strings.EqualFold(item.Hash, first.Hash) || item.AddedOn != first.AddedOn || item.RecentUploadBytes == nil || *item.RecentUploadBytes > first.RecentUploadBytes || item.UploadWindowSeconds != plan.Frozen.RecentUploadWindowSeconds {
			continue
		}
		if pool, known := ResolveStoragePool(config.PathMappings, plan.InstanceID, item.SavePath); !known || pool != plan.PoolID {
			return
		}
		actual, err := models.NormalizeRacingPath(item.SavePath)
		if err != nil {
			return
		}
		anchor := ""
		for _, mapping := range config.PathMappings {
			if mapping.InstanceID == plan.InstanceID && mapping.StoragePoolID == plan.PoolID && (actual == mapping.Path || strings.HasPrefix(actual, strings.TrimSuffix(mapping.Path, "/")+"/")) && len(mapping.Path) > len(anchor) {
				anchor = mapping.Path
			}
		}
		if anchor == "" {
			return
		}
		baseline, err := files.ReclaimReleaseBaseline(ctx, plan.InstanceID, item, poolInstances, anchor)
		if err != nil || baseline == nil || baseline.ExpectedBytes != first.CapacityBytes {
			return
		}
		// Capacity may have recovered or new promises may have changed the
		// deficit during file inspection. Either change requires reassessment.
		commitments, err := e.store.ReclaimCommitments(ctx, plan.PoolID)
		if err != nil {
			return
		}
		intents, err := e.allIntents(ctx)
		if err != nil {
			return
		}
		observations := reader.CachedExecutionObservations()
		budgets := executionBudgets(config, observations, intents, time.Now())
		targets := receptionTargetsWithCapacity(*selection.Rule, config, config.ExecutionPolicies, observations, intents, budgets, candidate.VerifiedMetadata.SizeBytes, time.Now(), true)
		targets = avoidDuplicateParticipants(targets, observations, candidate.VerifiedMetadata.HashV1, candidate.VerifiedMetadata.HashV2)
		for _, target := range targets {
			if target.instance.InstanceID == plan.InstanceID && len(target.pools) == 1 && target.pools[0] == plan.PoolID && candidate.VerifiedMetadata.SizeBytes-target.available == plan.DeficitBytes {
				// The common qB path repeats tracker/identity checks, then claims
				// the budget and sends once. An error never triggers another send.
				_ = client.ReclaimDelete(ctx, plan, *baseline, commitments, item.SavePath)
				return
			}
		}
		return
	}
}

func (e *executionRunner) launchReclaimReconciliation(ctx context.Context) {
	e.service.mu.RLock()
	client, ok := e.service.executionClient.(ReclaimExecutionClient)
	files, supported := e.service.reclaimReader.(ReclaimFilesystemReader)
	e.service.mu.RUnlock()
	if !ok || !supported {
		return
	}
	e.mu.Lock()
	if e.reclaimReleaseBusy || time.Since(e.reclaimReleaseAttempt) < 5*time.Second {
		e.mu.Unlock()
		return
	}
	e.reclaimReleaseBusy = true
	e.reclaimReleaseAttempt = time.Now()
	after := e.reclaimReleaseAfter
	// Bound each sweep so a stream of newer operations cannot indefinitely
	// postpone revisiting an older pending observation.
	if after == "" {
		e.reclaimReleaseBefore = time.Now()
	}
	before := e.reclaimReleaseBefore
	e.mu.Unlock()
	e.workers.Go(func() {
		defer func() { e.mu.Lock(); e.reclaimReleaseBusy = false; e.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		items, err := e.store.ReclaimReleasesToObserve(ctx, after, before)
		if err != nil {
			return
		}
		if len(items) == 0 {
			e.mu.Lock()
			e.reclaimReleaseAfter = ""
			e.mu.Unlock()
		}
		for _, item := range items {
			if ctx.Err() != nil {
				return
			}
			e.mu.Lock()
			e.reclaimReleaseAfter = item.OperationID
			e.mu.Unlock()
			if err := client.ReconcileAutomaticDeletes(ctx, item.InstanceID); err != nil {
				continue
			}
			space, recovered, err := files.ReclaimReleaseSpace(ctx, item.InstanceID, item.Baseline)
			if err == nil && recovered {
				_ = e.store.RecordReclaimRelease(ctx, item.OperationID, space)
			}
		}
	})
}
