// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/racing/sources"
)

func TestIdentityMergesBothArrivalOrdersWithoutResettingTime(t *testing.T) {
	c, _, now := ruleFixture()
	early := now.Add(-20 * time.Second)
	late := now.Add(-10 * time.Second)
	web := c.Item
	web.Title = "Example Aurora Complete Long Title"
	web.Free.Value = sources.Unknown
	rss := c.Item
	rss.Title = "Example Aurora"
	rss.Official.Value = sources.Unknown
	webJSON, err := json.Marshal(web)
	require.NoError(t, err)
	rssJSON, err := json.Marshal(rss)
	require.NoError(t, err)
	rows := []models.RacingDiscovery{{SourceID: 1, SiteID: 1, EventKey: c.EventKey, FirstSeenAt: early.Format(time.RFC3339Nano), Eligible: true, Item: webJSON}, {SourceID: 2, SiteID: 1, EventKey: c.EventKey, FirstSeenAt: late.Format(time.RFC3339Nano), Eligible: true, Item: rssJSON}}
	merged, err := MergeDiscoveries(rows)
	require.NoError(t, err)
	require.Equal(t, early, merged.FirstSeenAt)
	require.Equal(t, []int{1, 2}, merged.SourceIDs)
	require.Equal(t, sources.Yes, merged.Item.Official.Value)
	require.Equal(t, sources.Yes, merged.Item.Free.Value)
	require.Equal(t, "Example Aurora Complete Long Title", merged.Item.Title)
	slices.Reverse(rows)
	reverse, err := MergeDiscoveries(rows)
	require.NoError(t, err)
	require.Equal(t, merged, reverse)
	// Two sites reporting even the same hash retain separate site identities.
	other := rows[0]
	other.SiteID = 2
	_, err = MergeDiscoveries([]models.RacingDiscovery{rows[0], other})
	require.Error(t, err)
	a, err := MergeDiscoveries(rows[:1])
	require.NoError(t, err)
	b, err := MergeDiscoveries([]models.RacingDiscovery{other})
	require.NoError(t, err)
	require.NotEqual(t, a.Key, b.Key)
}

func TestIdentityConflictsStayUnknownAndRevivalSeparate(t *testing.T) {
	c, _, now := ruleFixture()
	left := c.Item
	right := c.Item
	right.Official.Value = sources.No
	left.ReportedHash = strings.Repeat("a", 40)
	right.ReportedHash = strings.Repeat("b", 40)
	size := int64(2048)
	right.SizeBytes = &size
	a, err := json.Marshal(left)
	require.NoError(t, err)
	b, err := json.Marshal(right)
	require.NoError(t, err)
	rows := []models.RacingDiscovery{{SourceID: 1, SiteID: 1, EventKey: c.EventKey, FirstSeenAt: now.Format(time.RFC3339Nano), Item: a}, {SourceID: 2, SiteID: 1, EventKey: c.EventKey, FirstSeenAt: now.Format(time.RFC3339Nano), Item: b}}
	merged, err := MergeDiscoveries(rows)
	require.NoError(t, err)
	require.Equal(t, sources.Unknown, merged.Item.Official.Value)
	require.Empty(t, merged.Item.ReportedHash)
	require.Nil(t, merged.Item.SizeBytes)
	require.ElementsMatch(t, []string{"official", "reported_hash", "size"}, merged.Conflicts)
	rows[1].EventKey = "torrent:42:revival:1234"
	_, err = MergeDiscoveries(rows)
	require.Error(t, err)
}

func TestVerifiedMetadataPreservesSourceHashConflicts(t *testing.T) {
	c, config, now := ruleFixture()
	proof := sources.VerifiedMetadata{Name: "Example Aurora", SizeBytes: 1024, HashV1: strings.Repeat("a", 40)}
	c.Conflicts = []string{"size", "reported_hash"}
	ApplyVerifiedMetadata(&c, proof)
	require.Equal(t, []string{"reported_hash"}, c.Conflicts)
	require.Equal(t, "waiting_metadata", SelectRule(c, config, now, nil).State)
	c, config, now = ruleFixture()
	c.Item.ReportedHash = strings.Repeat("b", 40)
	ApplyVerifiedMetadata(&c, proof)
	require.Contains(t, c.Conflicts, "verified_hash_mismatch")
	require.Equal(t, "waiting_metadata", SelectRule(c, config, now, nil).State)
	c, config, now = ruleFixture()
	c.Item.ReportedHash = proof.HashV1
	ApplyVerifiedMetadata(&c, proof)
	require.Empty(t, c.Conflicts)
	require.Equal(t, "ready", SelectRule(c, config, now, nil).State)
	another, _, _ := ruleFixture()
	another.SiteID = 2
	another.Key = "site:2:torrent:42:published"
	ApplyVerifiedMetadata(&another, proof)
	require.Equal(t, c.VerifiedMetadata.HashV1, another.VerifiedMetadata.HashV1)
	require.NotEqual(t, c.Key, another.Key, "same content across sites is not participation or a shared event")
}
