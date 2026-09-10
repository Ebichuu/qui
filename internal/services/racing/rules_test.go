// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/racing/sources"
)

func ruleFixture() (Candidate, *models.RacingConfiguration, time.Time) {
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	published := now.Add(-time.Minute)
	size := int64(1024)
	c := Candidate{Key: "site:1:torrent:42:published", SiteID: 1, EventKey: "torrent:42:published", FirstSeenAt: now.Add(-30 * time.Second), SourceIDs: []int{1, 2}, EligibleSourceIDs: []int{1, 2}, Item: sources.PublicItem{EventKey: "torrent:42:published", TorrentID: "42", Title: "Example Aurora Full Title", PublishedAt: &published, SizeBytes: &size, Official: sources.Evidence{Value: sources.Yes}, Free: sources.Evidence{Value: sources.Yes}, Revival: sources.Evidence{Value: sources.Yes}}}
	group := 11
	rule := models.RacingRule{ID: 1, Name: "Synthetic rule", Enabled: true, SourceIDs: []int{1}, AcceptKinds: []string{"free"}, ReceiveWindowSeconds: 900, TargetGroupID: &group}
	config := &models.RacingConfiguration{Sites: []models.RacingSite{{ID: 1, Enabled: true}}, Sources: []models.RacingSource{{ID: 1, SiteID: 1, Enabled: true}, {ID: 2, SiteID: 1, Enabled: true}}, Rules: []models.RacingRule{rule}}
	return c, config, now
}

func TestRuleTypeORAndOfficialPriority(t *testing.T) {
	for _, kinds := range [][]string{{"official"}, {"free"}, {"revival"}, {"official", "free", "revival"}} {
		c, config, now := ruleFixture()
		config.Rules[0].AcceptKinds = kinds
		result := SelectRule(c, config, now, nil)
		require.Equal(t, "ready", result.State)
		require.Equal(t, "official", result.Priority)
		require.ElementsMatch(t, kinds, result.MatchedKinds)
		require.Equal(t, 1, result.Rule.ID)
	}
	c, config, now := ruleFixture()
	c.Item.Free.Value = sources.No
	config.Rules[0].AcceptKinds = []string{"official", "free"}
	require.Equal(t, "ready", SelectRule(c, config, now, nil).State, "official OR free must accept known official without free")
	config.Rules[0].AcceptKinds = []string{}
	require.Equal(t, "rejected", SelectRule(c, config, now, nil).State)
}

func TestWinningRuleIsCompleteAndDoesNotFallback(t *testing.T) {
	c, config, now := ruleFixture()
	ordinary := config.Rules[0]
	ordinary.SortOrder = 0
	ordinary.AllowOfficialReclaim = true
	official := ordinary
	official.ID = 2
	official.SortOrder = 99
	official.AcceptKinds = []string{"official"}
	official.AllowOfficialReclaim = false
	group := 22
	official.TargetGroupID = &group
	official.ReceiveWindowSeconds = 120
	config.Rules = []models.RacingRule{ordinary, official}
	var checked []int
	result := SelectRule(c, config, now, func(rule models.RacingRule) bool { checked = append(checked, rule.ID); return rule.ID != 2 })
	require.Equal(t, "waiting_target", result.State)
	require.Equal(t, 2, result.Rule.ID)
	require.Equal(t, 22, *result.Rule.TargetGroupID)
	require.False(t, result.Rule.AllowOfficialReclaim)
	require.Equal(t, 120, result.Rule.ReceiveWindowSeconds)
	require.Equal(t, []int{2}, checked)
	require.Equal(t, c.Item.PublishedAt.Add(120*time.Second), *result.Deadline)
	config.Rules[1].Enabled = false
	result = SelectRule(c, config, now, nil)
	require.Equal(t, 1, result.Rule.ID)
	require.Equal(t, "official", result.Priority, "free-only rule receiving an official keeps official scheduling")
}

func TestUnknownLabelsAreNotFalseOrMatches(t *testing.T) {
	c, config, now := ruleFixture()
	c.Item.Official = sources.Evidence{Value: sources.Unknown}
	result := SelectRule(c, config, now, nil)
	require.Equal(t, []string{"free"}, result.MatchedKinds)
	require.Equal(t, "waiting_metadata", result.State)
	require.Equal(t, "unknown", result.Priority)
	require.Contains(t, result.MissingFields, "official")
	config.Rules[0].AcceptKinds = []string{"official"}
	result = SelectRule(c, config, now, nil)
	require.Empty(t, result.MatchedKinds)
	require.Equal(t, "waiting_metadata", result.State)
	c.Item.Official.Value = sources.No
	require.Equal(t, "rejected", SelectRule(c, config, now, nil).State)
}

func TestRuleFiltersAndWindow(t *testing.T) {
	c, config, now := ruleFixture()
	config.Rules[0].Filters.IncludeKeywords = []string{"aurora", "full"}
	config.Rules[0].Filters.ExcludeKeywords = []string{"exclude"}
	minimum, maximum := int64(1000), int64(2000)
	config.Rules[0].Filters.MinSizeBytes = &minimum
	config.Rules[0].Filters.MaxSizeBytes = &maximum
	require.Equal(t, "ready", SelectRule(c, config, now, nil).State)
	c.Item.SizeBytes = nil
	result := SelectRule(c, config, now, nil)
	require.Equal(t, "waiting_metadata", result.State)
	require.Contains(t, result.MissingFields, "size")
	c.Item.SizeBytes = &minimum
	c.Item.Title = "Example Aurora Exclude Full Title"
	require.Equal(t, "rejected", SelectRule(c, config, now, nil).State)
	c, config, now = ruleFixture()
	config.Rules[0].ReceiveWindowSeconds = 60
	require.Equal(t, "expired", SelectRule(c, config, now, nil).State, "deadline is exclusive")
	config.Rules[0].ReceiveWindowSeconds = 61
	require.Equal(t, "ready", SelectRule(c, config, now, nil).State)
	expiry := now.Add(-time.Second)
	c.Item.FreeExpiresAt = &expiry
	require.Equal(t, "rejected", SelectRule(c, config, now, nil).State)
	c.Item.FreeExpiresAt = nil
	require.Equal(t, "ready", SelectRule(c, config, now, nil).State, "missing promotion expiry alone must not block a reliable free claim")
}

func TestRuleSourceDisableAndStableOrder(t *testing.T) {
	c, config, now := ruleFixture()
	second := config.Rules[0]
	second.ID = 2
	second.SourceIDs = []int{2}
	second.SortOrder = -1
	config.Rules = append(config.Rules, second)
	require.Equal(t, 2, SelectRule(c, config, now, nil).Rule.ID)
	config.Sources[1].Enabled = false
	require.Equal(t, 1, SelectRule(c, config, now, nil).Rule.ID)
	config.Sites[0].Enabled = false
	require.Equal(t, "rejected", SelectRule(c, config, now, nil).State)
}

func TestExpiredWinnerCannotFallThroughToLongerFallbackWindow(t *testing.T) {
	c, config, now := ruleFixture()
	fallback := config.Rules[0]
	fallback.SortOrder = 0
	fallback.ReceiveWindowSeconds = 900
	official := fallback
	official.ID = 2
	official.AcceptKinds = []string{"official"}
	official.SortOrder = 99
	official.ReceiveWindowSeconds = 45
	config.Rules = []models.RacingRule{fallback, official}
	result := SelectRule(c, config, now, func(models.RacingRule) bool {
		t.Fatal("expired candidate must not check or allocate targets")
		return true
	})
	require.Equal(t, "expired", result.State)
	require.Equal(t, 2, result.Rule.ID)
}
