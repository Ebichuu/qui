// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

// RacingReclaimPolicy is shared by every RSS targeting a downloader. Saving it
// does not transfer deletion authority or start a reclaim executor.
type RacingReclaimPolicy struct {
	Enabled                   bool  `json:"enabled"`
	RuleIDs                   []int `json:"ruleIds"`
	MaxDeletes                int   `json:"maxDeletes"`
	MaxReclaimBytes           int64 `json:"maxReclaimBytes"`
	MaxRecentUploadBytes      int64 `json:"maxRecentUploadBytes"`
	RecentUploadWindowSeconds int   `json:"recentUploadWindowSeconds"`
	MaxOvershootBytes         int64 `json:"maxOvershootBytes"`
}

type RacingReclaimSetting struct {
	Scope    string              `json:"scope"`
	TargetID int                 `json:"targetId"`
	Policy   RacingReclaimPolicy `json:"policy"`
}

type RacingEffectiveReclaim struct {
	InstanceID int                  `json:"instanceId"`
	State      string               `json:"state"`
	GroupIDs   []int                `json:"groupIds"`
	Policy     *RacingReclaimPolicy `json:"policy,omitempty"`
}

type RacingReclaimConfiguration struct {
	Revision  int64                    `json:"revision"`
	Settings  []RacingReclaimSetting   `json:"settings"`
	Effective []RacingEffectiveReclaim `json:"effective"`
}
