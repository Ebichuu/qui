// Copyright (c) 2025-2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package automations

import (
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/autobrr/qui/internal/models"
)

type deleteConditionMatchKey struct {
	instanceID  int
	ruleID      int
	ruleVersion int64
	hash        string
	dryRun      bool
}

type deleteConditionMatchState struct {
	matchedSince time.Time
	lastSeen     time.Time
	duration     time.Duration
}

func deleteConditionDuration(rule *models.Automation) time.Duration {
	if rule == nil || rule.Conditions == nil || rule.Conditions.Delete == nil {
		return 0
	}
	seconds := rule.Conditions.Delete.ConditionMatchDurationSeconds
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func newDeleteConditionMatchKey(instanceID int, rule *models.Automation, torrent qbt.Torrent, dryRun bool) deleteConditionMatchKey {
	return deleteConditionMatchKey{
		instanceID:  instanceID,
		ruleID:      rule.ID,
		ruleVersion: rule.UpdatedAt.UnixNano(),
		hash:        torrent.Hash,
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
) bool {
	duration := deleteConditionDuration(rule)
	if duration <= 0 {
		return matched
	}

	key := newDeleteConditionMatchKey(instanceID, rule, torrent, dryRun)
	seen[key] = struct{}{}
	ready := s.deleteConditionReady(now, key, duration, matched)
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
	if !ok {
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
	activeRules := make(map[int]int64)
	for _, rule := range rules {
		if deleteConditionDuration(rule) > 0 {
			activeRules[rule.ID] = rule.UpdatedAt.UnixNano()
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
