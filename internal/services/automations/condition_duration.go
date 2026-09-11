// Copyright (c) 2025-2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package automations

import (
	"crypto/sha256"
	"encoding/json"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/autobrr/qui/internal/models"
)

type deleteConditionMatchKey struct {
	instanceID  int
	ruleID      int
	ruleVersion [32]byte
	addedOn     int64
	hash        string
	dryRun      bool
}

type deleteConditionMatchState struct {
	matchedSince time.Time
	lastSeen     time.Time
	duration     time.Duration
	uploaded     int64
	downloaded   int64
	restored     bool
}

func deleteConditionDuration(rule *models.Automation) time.Duration {
	if rule == nil || rule.Conditions == nil || rule.Conditions.Delete == nil {
		return 0
	}
	seconds := rule.Conditions.Delete.ConditionMatchDurationSeconds
	if seconds <= 0 {
		return 0
	}
	if int64(seconds) > int64((1<<63-1)/time.Second) {
		return time.Duration(1<<63 - 1)
	}
	return time.Duration(seconds) * time.Second
}

func newDeleteConditionMatchKey(instanceID int, rule *models.Automation, torrent qbt.Torrent, dryRun bool, version [32]byte) deleteConditionMatchKey {
	return deleteConditionMatchKey{
		instanceID:  instanceID,
		ruleID:      rule.ID,
		ruleVersion: version,
		hash:        torrent.Hash,
		addedOn:     torrent.AddedOn,
		dryRun:      dryRun,
	}
}

func deleteConditionMonitoringRule(rule *models.Automation) *models.Automation {
	if rule == nil || rule.Conditions == nil || rule.Conditions.Delete == nil {
		return nil
	}

	monitoringRule := *rule
	monitoringRule.Conditions = &models.ActionConditions{
		SchemaVersion: rule.Conditions.SchemaVersion,
		Grouping:      rule.Conditions.Grouping,
		Delete:        rule.Conditions.Delete,
	}
	return &monitoringRule
}

func (s *Service) deleteConditionReadyForRule(
	now time.Time,
	instanceID int,
	rule *models.Automation,
	torrent qbt.Torrent,
	dryRun bool,
	matched bool,
	suppressedRuleIDs map[int]struct{},
	seen map[deleteConditionMatchKey]struct{},
	version [32]byte,
) bool {
	duration := deleteConditionDuration(rule)
	if duration <= 0 {
		return matched
	}

	key := newDeleteConditionMatchKey(instanceID, rule, torrent, dryRun, version)
	seen[key] = struct{}{}
	interval := DefaultRuleInterval
	if rule.IntervalSeconds != nil {
		interval = time.Duration(*rule.IntervalSeconds) * time.Second
	}
	s.mu.Lock()
	if state, ok := s.deleteConditionMatches[key]; ok && (now.Before(state.lastSeen) || now.Sub(state.lastSeen) > 2*interval || torrent.Uploaded < state.uploaded || torrent.Downloaded < state.downloaded) {
		delete(s.deleteConditionMatches, key)
	} else if ok && state.restored {
		// Preserve measured time, but never credit the unobserved restart gap.
		state.matchedSince = state.matchedSince.Add(now.Sub(state.lastSeen))
		state.lastSeen = now
		state.restored = false
		s.deleteConditionMatches[key] = state
	}
	s.mu.Unlock()
	ready := s.deleteConditionReady(now, key, duration, matched)
	s.mu.Lock()
	if state, ok := s.deleteConditionMatches[key]; ok {
		state.uploaded, state.downloaded = torrent.Uploaded, torrent.Downloaded
		s.deleteConditionMatches[key] = state
	}
	s.mu.Unlock()
	if _, suppressed := suppressedRuleIDs[rule.ID]; suppressed {
		return false
	}
	return ready
}

func (s *Service) deleteConditionReady(now time.Time, key deleteConditionMatchKey, duration time.Duration, matched bool) bool {
	if duration <= 0 {
		return matched
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteConditionMatches == nil {
		s.deleteConditionMatches = make(map[deleteConditionMatchKey]deleteConditionMatchState)
	}

	if !matched {
		delete(s.deleteConditionMatches, key)
		return false
	}

	state, ok := s.deleteConditionMatches[key]
	if !ok || state.duration != duration || now.Before(state.lastSeen) {
		s.deleteConditionMatches[key] = deleteConditionMatchState{
			matchedSince: now,
			lastSeen:     now,
			duration:     duration,
		}
		return false
	}

	state.lastSeen = now
	s.deleteConditionMatches[key] = state
	return now.Sub(state.matchedSince) >= duration
}

func (s *Service) pruneDeleteConditionMatches(instanceID int, rules []*models.Automation, dryRun bool, seen map[deleteConditionMatchKey]struct{}) {
	activeRules := make(map[int][32]byte)
	for _, rule := range rules {
		if deleteConditionDuration(rule) > 0 {
			activeRules[rule.ID] = deleteConditionRuleVersion(rule)
		}
	}
	if len(activeRules) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.deleteConditionMatches {
		version, active := activeRules[key.ruleID]
		if key.instanceID != instanceID || key.dryRun != dryRun || !active {
			continue
		}
		if key.ruleVersion != version {
			delete(s.deleteConditionMatches, key)
			continue
		}
		if _, ok := seen[key]; !ok {
			delete(s.deleteConditionMatches, key)
		}
	}
}

// Reconcile against the full configuration, before interval/cooldown filtering.
// Pruning an eligible subset must not erase timers for rules not due this tick.
func (s *Service) reconcileDeleteConditionRules(instanceID int, rules []*models.Automation) {
	active := make(map[int]struct {
		version [32]byte
		dryRun  bool
	})
	for _, rule := range rules {
		if rule.Enabled && rule.Conditions != nil && rule.Conditions.Delete != nil && rule.Conditions.Delete.Enabled && deleteConditionDuration(rule) > 0 {
			active[rule.ID] = struct {
				version [32]byte
				dryRun  bool
			}{deleteConditionRuleVersion(rule), rule.DryRun}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.deleteConditionMatches {
		if key.instanceID != instanceID {
			continue
		}
		rule, ok := active[key.ruleID]
		if !ok || rule.dryRun != key.dryRun || rule.version != key.ruleVersion {
			delete(s.deleteConditionMatches, key)
		}
	}
}

// SQLite's legacy update trigger has second precision. Include the deletion
// definition so edits within the same second cannot inherit a previous timer.
func deleteConditionRuleVersion(rule *models.Automation) [32]byte {
	data, _ := json.Marshal(deleteConditionMonitoringRule(rule))
	return sha256.Sum256(data)
}
