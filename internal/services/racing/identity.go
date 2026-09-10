// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/racing/sources"
)

// Candidate is a site event, not a content hash. Even verified equal hashes
// across sites retain separate site/account and eventual tracker participation.
type Candidate struct {
	VerifiedMetadata  *sources.VerifiedMetadata `json:"verifiedMetadata,omitempty"`
	Key               string                    `json:"key"`
	SiteID            int                       `json:"siteId"`
	EventKey          string                    `json:"eventKey"`
	Item              sources.PublicItem        `json:"item"`
	FirstSeenAt       time.Time                 `json:"firstSeenAt"`
	SourceIDs         []int                     `json:"sourceIds"`
	EligibleSourceIDs []int                     `json:"eligibleSourceIds"`
	Conflicts         []string                  `json:"conflicts"`
}

// MergeDiscoveries only correlates equal site event keys. Unknown evidence
// cannot erase known evidence; contradictory positive/negative claims remain
// unknown until a later authoritative observation resolves the conflict.
func MergeDiscoveries(rows []models.RacingDiscovery) (*Candidate, error) {
	if len(rows) == 0 {
		return nil, errors.New("no source observations")
	}
	candidate := &Candidate{SiteID: rows[0].SiteID, EventKey: rows[0].EventKey, SourceIDs: []int{}, EligibleSourceIDs: []int{}, Conflicts: []string{}}
	candidate.Key = "site:" + strconv.Itoa(candidate.SiteID) + ":" + candidate.EventKey
	var official, free, revival []sources.Evidence
	var sizes, approximateSizes []int64
	var hashes []string
	var published []time.Time
	for _, row := range rows {
		if row.SiteID != candidate.SiteID || row.EventKey != candidate.EventKey {
			return nil, errors.New("observations belong to different site events")
		}
		var item sources.PublicItem
		if err := json.Unmarshal(row.Item, &item); err != nil {
			return nil, errors.New("invalid source observation")
		}
		if item.EventKey != row.EventKey {
			return nil, errors.New("source event identity mismatch")
		}
		first, err := time.Parse(time.RFC3339Nano, row.FirstSeenAt)
		if err != nil {
			return nil, errors.New("invalid first discovery time")
		}
		if candidate.FirstSeenAt.IsZero() || first.Before(candidate.FirstSeenAt) {
			candidate.FirstSeenAt = first
		}
		candidate.SourceIDs = append(candidate.SourceIDs, row.SourceID)
		if row.Eligible {
			candidate.EligibleSourceIDs = append(candidate.EligibleSourceIDs, row.SourceID)
		}
		if len(item.Title) > len(candidate.Item.Title) {
			candidate.Item.Title = item.Title
		}
		if candidate.Item.TorrentID == "" {
			candidate.Item.TorrentID = item.TorrentID
		} else if item.TorrentID != "" && item.TorrentID != candidate.Item.TorrentID {
			candidate.Conflicts = append(candidate.Conflicts, "identity")
		}
		if item.SizeBytes != nil {
			if item.SizeApproximate {
				approximateSizes = append(approximateSizes, *item.SizeBytes)
			} else {
				sizes = append(sizes, *item.SizeBytes)
			}
		}
		if item.ReportedHash != "" {
			hashes = append(hashes, item.ReportedHash)
		}
		if item.PublishedAt != nil {
			published = append(published, *item.PublishedAt)
		}
		if item.OriginalPublishedAt != nil && (candidate.Item.OriginalPublishedAt == nil || item.OriginalPublishedAt.Before(*candidate.Item.OriginalPublishedAt)) {
			candidate.Item.OriginalPublishedAt = item.OriginalPublishedAt
		}
		if item.FreeExpiresAt != nil && (candidate.Item.FreeExpiresAt == nil || item.FreeExpiresAt.Before(*candidate.Item.FreeExpiresAt)) {
			candidate.Item.FreeExpiresAt = item.FreeExpiresAt
		}
		official = append(official, item.Official)
		free = append(free, item.Free)
		revival = append(revival, item.Revival)
	}
	candidate.Item.EventKey = candidate.EventKey
	candidate.Item.Official = mergeEvidence(official)
	candidate.Item.Free = mergeEvidence(free)
	candidate.Item.Revival = mergeEvidence(revival)
	for name, evidence := range map[string]sources.Evidence{"official": candidate.Item.Official, "free": candidate.Item.Free, "revival": candidate.Item.Revival} {
		if evidence.Basis == "conflicting_sources" {
			candidate.Conflicts = append(candidate.Conflicts, name)
		}
	}
	// Different sizes may be rounding or different metadata. Without explicit
	// precision evidence, do not manufacture an exact size from a tolerance.
	if len(sizes) == 0 {
		sizes = approximateSizes
		candidate.Item.SizeApproximate = len(sizes) > 0
	}
	slices.Sort(sizes)
	if len(sizes) > 0 {
		small, large := sizes[0], sizes[len(sizes)-1]
		if large != small {
			candidate.Conflicts = append(candidate.Conflicts, "size")
		} else {
			candidate.Item.SizeBytes = &large
		}
	}
	slices.Sort(hashes)
	hashes = slices.Compact(hashes)
	if len(hashes) == 1 {
		candidate.Item.ReportedHash = hashes[0]
	} else if len(hashes) > 1 {
		candidate.Conflicts = append(candidate.Conflicts, "reported_hash")
	}
	// Time differences matter to the participation window. Never move a deadline
	// later merely because a later source supplied a more recent timestamp.
	if len(published) > 0 {
		slices.SortFunc(published, func(a, b time.Time) int { return a.Compare(b) })
		candidate.Item.PublishedAt = &published[0]
	}
	if slices.Contains(candidate.Conflicts, "identity") {
		candidate.Item.TorrentID = ""
	}
	slices.Sort(candidate.SourceIDs)
	candidate.SourceIDs = slices.Compact(candidate.SourceIDs)
	slices.Sort(candidate.EligibleSourceIDs)
	candidate.EligibleSourceIDs = slices.Compact(candidate.EligibleSourceIDs)
	slices.Sort(candidate.Conflicts)
	candidate.Conflicts = slices.Compact(candidate.Conflicts)
	return candidate, nil
}

func mergeEvidence(items []sources.Evidence) sources.Evidence {
	result := sources.Evidence{Value: sources.Unknown}
	for _, item := range items {
		if item.Value != sources.Yes && item.Value != sources.No {
			continue
		}
		if result.Value != sources.Unknown && result.Value != item.Value {
			return sources.Evidence{Value: sources.Unknown, Basis: "conflicting_sources"}
		}
		result = item
	}
	return result
}

// ApplyVerifiedMetadata keeps conflicting source identities visible. Verified
// file bytes establish content; they cannot silently erase contradictory claims
// about which file a site's event is supposed to reference.
func ApplyVerifiedMetadata(candidate *Candidate, metadata sources.VerifiedMetadata) {
	candidate.VerifiedMetadata = &metadata
	candidate.Item.SizeBytes = &metadata.SizeBytes
	candidate.Item.SizeApproximate = false
	candidate.Item.Title = metadata.Name
	candidate.Conflicts = slices.DeleteFunc(candidate.Conflicts, func(field string) bool {
		return field == "size" || field == "title" || field == "verified_hash_mismatch"
	})
	if hash := candidate.Item.ReportedHash; hash != "" && hash != metadata.HashV1 && hash != metadata.HashV2 {
		candidate.Conflicts = append(candidate.Conflicts, "verified_hash_mismatch")
	}
}
