// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

// Racing configuration belongs to qui, independently of qBittorrent RSS rules.
// Enabling configuration in Q1 never grants permission to add or delete torrents.
type RacingSite struct {
	ID                     int      `json:"id"`
	Name                   string   `json:"name"`
	BaseURL                string   `json:"baseUrl"`
	Enabled                bool     `json:"enabled"`
	TrackerHosts           []string `json:"trackerHosts"`
	RequestIntervalSeconds int      `json:"requestIntervalSeconds"`
	HasCredential          bool     `json:"hasCredential"`
	UpdatedAt              string   `json:"updatedAt"`
}

type RacingSiteInput struct {
	Name                   string   `json:"name"`
	BaseURL                string   `json:"baseUrl"`
	Enabled                bool     `json:"enabled"`
	TrackerHosts           []string `json:"trackerHosts"`
	RequestIntervalSeconds int      `json:"requestIntervalSeconds"`
	// Omitted preserves the existing secret; empty explicitly clears it.
	Credential *string `json:"credential,omitempty"`
}

type RacingSource struct {
	ID              int    `json:"id"`
	SiteID          int    `json:"siteId"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Enabled         bool   `json:"enabled"`
	IntervalSeconds int    `json:"intervalSeconds"`
	// Never return the endpoint path, user info or query (which may contain keys).
	URLOrigin string `json:"urlOrigin"`
	UpdatedAt string `json:"updatedAt"`
}

type RacingSourceInput struct {
	SiteID          int     `json:"siteId"`
	Name            string  `json:"name"`
	Kind            string  `json:"kind"`
	Enabled         bool    `json:"enabled"`
	IntervalSeconds int     `json:"intervalSeconds"`
	URL             *string `json:"url,omitempty"`
}

type RacingGroupInput struct {
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	InstanceIDs []int  `json:"instanceIds"`
}

type RacingGroup struct {
	ID int `json:"id"`
	RacingGroupInput
	UpdatedAt string `json:"updatedAt"`
}

// Each accepted kind is an OR entry. Other filters are AND constraints.
// Candidate official/free/revival evidence will retain unknown independently.
type RacingRuleFilters struct {
	MinSizeBytes    *int64   `json:"minSizeBytes,omitempty"`
	MaxSizeBytes    *int64   `json:"maxSizeBytes,omitempty"`
	IncludeKeywords []string `json:"includeKeywords"`
	ExcludeKeywords []string `json:"excludeKeywords"`
}

type RacingRuleInput struct {
	Name        string            `json:"name"`
	Enabled     bool              `json:"enabled"`
	SortOrder   int               `json:"sortOrder"`
	SourceIDs   []int             `json:"sourceIds"`
	AcceptKinds []string          `json:"acceptKinds"`
	Filters     RacingRuleFilters `json:"filters"`
	// Explicit per-rule window; there is no global three-minute default.
	ReceiveWindowSeconds int  `json:"receiveWindowSeconds"`
	TargetGroupID        *int `json:"targetGroupId,omitempty"`
	TargetInstanceID     *int `json:"targetInstanceId,omitempty"`
	// Configuration only. Q1 cannot reclaim or acquire deletion ownership.
	AllowOfficialReclaim bool `json:"allowOfficialReclaim"`
}

type RacingRule struct {
	ID int `json:"id"`
	RacingRuleInput
	UpdatedAt string `json:"updatedAt"`
}

type RacingConfiguration struct {
	Sites   []RacingSite   `json:"sites"`
	Sources []RacingSource `json:"sources"`
	Groups  []RacingGroup  `json:"groups"`
	Rules   []RacingRule   `json:"rules"`
}
