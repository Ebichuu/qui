// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package automations

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/autobrr/qui/internal/models"
)

func (s *Service) lockInstanceRun(instanceID int) func() {
	value, _ := s.instanceRuns.LoadOrStore(instanceID, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

// Called under the instance run lock before evaluating any rule.
func (s *Service) restoreConditionObservations(ctx context.Context, instanceID int) error {
	s.mu.RLock()
	restored := s.restoredInstances[instanceID]
	s.mu.RUnlock()
	if s.ruleStore == nil || restored {
		return nil
	}
	items, err := s.ruleStore.ConditionObservations(ctx, instanceID)
	if err != nil {
		return err
	}
	cooldown, err := s.ruleStore.DeleteCooldown(ctx, instanceID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteConditionMatches == nil {
		s.deleteConditionMatches = make(map[deleteConditionMatchKey]deleteConditionMatchState)
	}
	for _, item := range items {
		version, err := hex.DecodeString(item.RuleVersion)
		if err != nil || len(version) != 32 {
			return errors.New("invalid persisted condition version")
		}
		key := deleteConditionMatchKey{instanceID: instanceID, ruleID: item.RuleID, ruleVersion: [32]byte(version), hash: item.Hash, addedOn: item.AddedOn, dryRun: item.DryRun}
		s.deleteConditionMatches[key] = deleteConditionMatchState{matchedSince: item.ObservedAt.Add(-item.Elapsed), lastSeen: item.ObservedAt, duration: item.Duration, uploaded: item.Uploaded, downloaded: item.Downloaded, restored: true}
	}
	if s.lastFreeSpaceDeleteAt == nil {
		s.lastFreeSpaceDeleteAt = make(map[int]time.Time)
	}
	s.lastFreeSpaceDeleteAt[instanceID] = cooldown
	if s.restoredInstances == nil {
		s.restoredInstances = make(map[int]bool)
	}
	s.restoredInstances[instanceID] = true
	return nil
}

func (s *Service) checkpointConditionObservations(ctx context.Context, instanceID int) error {
	if s.ruleStore == nil {
		return nil
	}
	s.mu.RLock()
	items := []models.AutomationConditionObservation{}
	for key, state := range s.deleteConditionMatches {
		if key.instanceID != instanceID || key.ruleID >= dryRunEphemeralRuleIDBase {
			continue
		}
		items = append(items, models.AutomationConditionObservation{RuleID: key.ruleID, RuleVersion: hex.EncodeToString(key.ruleVersion[:]), Hash: key.hash, AddedOn: key.addedOn, DryRun: key.dryRun, Elapsed: state.lastSeen.Sub(state.matchedSince), Duration: state.duration, ObservedAt: state.lastSeen, Uploaded: state.uploaded, Downloaded: state.downloaded})
	}
	s.mu.RUnlock()
	return s.ruleStore.SaveConditionObservations(ctx, instanceID, items)
}
