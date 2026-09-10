// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/racing/sources"
)

type RuleSelection struct {
	State         string             `json:"state"`
	Reason        string             `json:"reason"`
	Priority      string             `json:"priority"`
	Rule          *models.RacingRule `json:"rule,omitempty"`
	MatchedKinds  []string           `json:"matchedKinds"`
	MissingFields []string           `json:"missingFields"`
	Deadline      *time.Time         `json:"deadline,omitempty"`
}

type ruleMatch struct {
	rule     models.RacingRule
	official bool
	matched  []string
	missing  []string
	deadline *time.Time
}

// SelectRule chooses one complete rule before checking its target. A temporarily
// unavailable winning group never causes fallback to another rule or target.
// It only evaluates read-only intent; it does not create or replace allocations.
func SelectRule(candidate Candidate, config *models.RacingConfiguration, now time.Time, targetAvailable func(models.RacingRule) bool) RuleSelection {
	result := RuleSelection{State: "rejected", Reason: "no_matching_rule", Priority: "unknown", MatchedKinds: []string{}, MissingFields: []string{}}
	switch candidate.Item.Official.Value {
	case sources.Yes:
		result.Priority = "official"
	case sources.No:
		result.Priority = "normal"
	default:
		result.Priority = "unknown"
	}
	var matches []ruleMatch
	for _, rule := range config.Rules {
		if !rule.Enabled || !candidateSourceEnabled(candidate, rule, config) {
			continue
		}
		match, ok := matchRule(candidate, rule, now)
		if ok {
			matches = append(matches, match)
		}
	}
	if len(matches) == 0 {
		return result
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].official != matches[j].official {
			return matches[i].official
		}
		if matches[i].rule.SortOrder != matches[j].rule.SortOrder {
			return matches[i].rule.SortOrder < matches[j].rule.SortOrder
		}
		return false
	})
	winner := matches[0]
	result.Rule = &winner.rule
	result.MatchedKinds = winner.matched
	result.MissingFields = winner.missing
	result.Deadline = winner.deadline
	if result.Deadline != nil && !now.Before(*result.Deadline) {
		result.State = "expired"
		result.Reason = "selected_window_closed"
		return result
	}
	// Identity/official uncertainty may change routing or scheduling. Known free
	// still matches, but cannot silently classify an unknown official as ordinary.
	if candidate.Item.TorrentID == "" || slices.Contains(candidate.Conflicts, "identity") {
		result.MissingFields = append(result.MissingFields, "identity")
	}
	if candidate.Item.Official.Value == sources.Unknown {
		result.MissingFields = append(result.MissingFields, "official")
	}
	for _, field := range candidate.Conflicts {
		if field == "reported_hash" || field == "verified_hash_mismatch" || field == "published_at" {
			result.MissingFields = append(result.MissingFields, field)
		}
	}
	slices.Sort(result.MissingFields)
	result.MissingFields = slices.Compact(result.MissingFields)
	if len(result.MissingFields) > 0 {
		result.State = "waiting_metadata"
		result.Reason = "required_evidence_missing"
		return result
	}
	if targetAvailable != nil && !targetAvailable(winner.rule) {
		result.State = "waiting_target"
		result.Reason = "selected_target_unavailable"
		return result
	}
	result.State = "ready"
	result.Reason = "rule_matched"
	return result
}

func candidateSourceEnabled(candidate Candidate, rule models.RacingRule, config *models.RacingConfiguration) bool {
	siteEnabled := false
	for _, site := range config.Sites {
		if site.ID == candidate.SiteID {
			siteEnabled = site.Enabled
			break
		}
	}
	if !siteEnabled {
		return false
	}
	for _, source := range config.Sources {
		if source.Enabled && source.SiteID == candidate.SiteID && slices.Contains(candidate.EligibleSourceIDs, source.ID) && slices.Contains(rule.SourceIDs, source.ID) {
			return true
		}
	}
	return false
}

func matchRule(candidate Candidate, rule models.RacingRule, now time.Time) (ruleMatch, bool) {
	match := ruleMatch{rule: rule, matched: []string{}, missing: []string{}}
	if rule.ReceiveWindowSeconds <= 0 || len(rule.AcceptKinds) == 0 {
		return match, false
	}
	item := candidate.Item
	if item.PublishedAt == nil || item.PublishedAt.After(now) {
		match.missing = append(match.missing, "published_at")
	} else {
		deadline := item.PublishedAt.Add(time.Duration(rule.ReceiveWindowSeconds) * time.Second)
		match.deadline = &deadline
		if !candidate.FirstSeenAt.IsZero() && !candidate.FirstSeenAt.Before(deadline) {
			return match, false
		}
	}
	title := normalKeyword(item.Title)
	if title == "" {
		match.missing = append(match.missing, "title")
	}
	for _, keyword := range rule.Filters.IncludeKeywords {
		if title != "" && !strings.Contains(title, normalKeyword(keyword)) {
			return match, false
		}
	}
	for _, keyword := range rule.Filters.ExcludeKeywords {
		if strings.Contains(title, normalKeyword(keyword)) {
			return match, false
		}
	}
	if rule.Filters.MinSizeBytes != nil || rule.Filters.MaxSizeBytes != nil {
		if item.SizeBytes == nil || item.SizeApproximate || slices.Contains(candidate.Conflicts, "size") {
			match.missing = append(match.missing, "size")
		} else if (rule.Filters.MinSizeBytes != nil && *item.SizeBytes < *rule.Filters.MinSizeBytes) || (rule.Filters.MaxSizeBytes != nil && *item.SizeBytes > *rule.Filters.MaxSizeBytes) {
			return match, false
		}
	}
	free := item.Free.Value
	if free == sources.Yes && item.FreeExpiresAt != nil && !now.Before(*item.FreeExpiresAt) {
		free = sources.No
	}
	var unknown []string
	for _, kind := range rule.AcceptKinds {
		var truth sources.Truth
		switch kind {
		case "official":
			truth = item.Official.Value
		case "free":
			truth = free
		case "revival":
			truth = item.Revival.Value
		default:
			continue
		}
		switch truth {
		case sources.Yes:
			match.matched = append(match.matched, kind)
		case sources.Unknown:
			unknown = append(unknown, kind)
		default:
			continue
		}
	}
	if len(match.matched) == 0 {
		if len(unknown) == 0 {
			return match, false
		}
		match.missing = append(match.missing, unknown...)
	}
	match.official = item.Official.Value == sources.Yes && slices.Contains(rule.AcceptKinds, "official")
	return match, true
}

func normalKeyword(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
