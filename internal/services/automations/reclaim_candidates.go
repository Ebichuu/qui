// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package automations

import (
	"context"
	"errors"
	"slices"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/autobrr/qui/internal/models"
)

type ReclaimCandidate struct {
	RecentUploadBytes    *int64    `json:"recentUploadBytes,omitempty"`
	UploadWindowSeconds  int       `json:"uploadWindowSeconds"`
	LowEfficiencySeconds int64     `json:"lowEfficiencySeconds"`
	Hash                 string    `json:"hash"`
	AddedOn              int64     `json:"addedOn"`
	SavePath             string    `json:"savePath"`
	SizeBytes            int64     `json:"sizeBytes"`
	UploadedBytes        int64     `json:"uploadedBytes"`
	RuleID               int       `json:"ruleId"`
	ObservedAt           time.Time `json:"observedAt"`
}

type ReclaimCandidates struct {
	InstanceID         int                `json:"instanceId"`
	Revision           int64              `json:"revision"`
	State              string             `json:"state"`
	Candidates         []ReclaimCandidate `json:"candidates"`
	UnavailableRuleIDs []int              `json:"unavailableRuleIds"`
}

func (s *Service) SetReclaimStore(store *models.RacingStore) { s.reclaimStore = store }

// Each observer retains only measured condition timers. It reuses the same
// evaluator, shared qB cache and existing dependency loaders, has no scheduler,
// and returns before the action/notification pipeline.
func (s *Service) reclaimObserver(instanceID int) *Service {
	if existing, ok := s.reclaimObservers.Load(instanceID); ok {
		return existing.(*Service)
	}
	observer := NewService(s.cfg, s.instanceStore, s.ruleStore, nil, s.trackerCustomizationStore, s.syncManager, nil, nil, s.crossMatcher, s.backendPool)
	observer.reclaimObservation = true
	actual, _ := s.reclaimObservers.LoadOrStore(instanceID, observer)
	return actual.(*Service)
}

// ObserveReclaimCandidates is read-only with respect to downloaders. Persisting
// an observation never reserves a candidate or authorizes a delete request.
func (s *Service) ObserveReclaimCandidates(ctx context.Context, instanceID int) (result *ReclaimCandidates, resultErr error) {
	if s.reclaimStore == nil {
		return nil, errors.New("reclaim configuration unavailable")
	}
	unlock := s.lockInstanceRun(instanceID)
	defer unlock()
	config, err := s.reclaimStore.ReclaimConfiguration(ctx)
	if err != nil {
		return nil, err
	}
	result = &ReclaimCandidates{InstanceID: instanceID, Revision: config.Revision, State: "unconfigured", Candidates: []ReclaimCandidate{}, UnavailableRuleIDs: []int{}}
	var policy *models.RacingReclaimPolicy
	for _, item := range config.Effective {
		if item.InstanceID == instanceID {
			result.State = item.State
			policy = item.Policy
			break
		}
	}
	observer := s.reclaimObserver(instanceID)
	if err := observer.restoreConditionObservations(ctx, instanceID); err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			observer.deleteConditionMatches = make(map[deleteConditionMatchKey]deleteConditionMatchState)
		}
		resultErr = errors.Join(resultErr, observer.checkpointConditionObservations(ctx, instanceID))
	}()
	if err := s.ruleStore.SaveReclaimObservations(ctx, instanceID, nil); err != nil {
		return nil, err
	}
	instance, err := s.instanceStore.Get(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if policy == nil || !policy.Enabled || !instance.IsActive {
		observer.deleteConditionMatches = make(map[deleteConditionMatchKey]deleteConditionMatchState)
		return result, nil
	}
	rules := []*models.Automation{}
	for _, id := range policy.RuleIDs {
		definition, err := s.ruleStore.GetDefinition(ctx, id)
		if err != nil {
			return nil, err
		}
		rule := reclaimRule(definition, instanceID)
		if rule == nil {
			result.UnavailableRuleIDs = append(result.UnavailableRuleIDs, id)
			continue
		}
		rules = append(rules, rule)
	}
	observer.reconcileDeleteConditionRules(instanceID, rules)
	if len(rules) == 0 {
		result.State = "conditions_unavailable"
		return result, nil
	}
	var sampleErr error
	observer.candidateCollector = func(states map[string]*torrentDesiredState, torrents []qbt.Torrent) {
		now := observer.reclaimObservedAt.UTC()
		counters := []models.ReclaimUploadCounter{}
		for _, torrent := range torrents {
			if torrent.AddedOn > 0 && torrent.Uploaded >= 0 && torrent.Size > 0 && torrent.Completed >= torrent.Size && torrent.AmountLeft == 0 {
				counters = append(counters, models.ReclaimUploadCounter{Hash: torrent.Hash, AddedOn: torrent.AddedOn, Uploaded: torrent.Uploaded})
			}
		}
		windows, err := s.ruleStore.CaptureReclaimUploads(ctx, instanceID, now, time.Duration(policy.RecentUploadWindowSeconds)*time.Second, 2*observer.cfg.ScanInterval, counters)
		if err != nil {
			sampleErr = err
			return
		}
		durations := map[string]int64{}
		for key, match := range observer.deleteConditionMatches {
			if state := states[key.hash]; state != nil && state.deleteRuleID == key.ruleID {
				durations[key.hash] = int64(match.lastSeen.Sub(match.matchedSince) / time.Second)
			}
		}
		for _, torrent := range torrents {
			if state := states[torrent.Hash]; state != nil && state.shouldDelete {
				candidate := ReclaimCandidate{Hash: torrent.Hash, AddedOn: torrent.AddedOn, SavePath: torrent.SavePath, SizeBytes: torrent.Size, UploadedBytes: torrent.Uploaded, RuleID: state.deleteRuleID, ObservedAt: now, UploadWindowSeconds: policy.RecentUploadWindowSeconds}
				if window := windows[torrent.Hash]; window.Covered {
					candidate.RecentUploadBytes = &window.Bytes
				}
				candidate.LowEfficiencySeconds = durations[torrent.Hash]
				result.Candidates = append(result.Candidates, candidate)
			}
		}
	}
	defer func() { observer.candidateCollector = nil }()
	if _, err := observer.applyRulesForInstance(ctx, instanceID, true, rules, true); err != nil {
		return nil, err
	}
	if sampleErr != nil {
		return nil, sampleErr
	}
	slices.SortFunc(result.Candidates, func(a, b ReclaimCandidate) int {
		if a.Hash < b.Hash {
			return -1
		}
		if a.Hash > b.Hash {
			return 1
		}
		return 0
	})
	return result, nil
}

func reclaimRule(definition *models.Automation, instanceID int) *models.Automation {
	if definition == nil || !definition.Enabled || definition.DryRun || definition.Conditions == nil || definition.Conditions.Delete == nil {
		return nil
	}
	action := definition.Conditions.Delete
	if (action.Usage != "official" && action.Usage != "both") || !action.Enabled || action.Condition == nil || action.ConditionMatchDurationSeconds < 60 || ConditionUsesField(action.Condition, FieldFreeSpace) {
		return nil
	}
	// Keep-files and cross-seed expansion do not provide a single-target release
	// estimate. They must be explicitly adapted before official reclaim can use them.
	if action.Mode != models.DeleteModeWithFiles || action.IncludeHardlinks || action.GroupID != "" || action.Atomic != "" {
		return nil
	}
	rule := deleteConditionMonitoringRule(definition)
	copied := *action
	copied.DailyTrigger = nil
	copied.Usage = "daily"
	rule.Conditions.Delete = &copied
	rule.InstanceID = instanceID
	rule.DryRun = true
	return rule
}
