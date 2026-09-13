// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import "time"

// RacingInstancePolicy opts one downloader into new reception. Existing Q1
// sources and rules alone never acquire automatic-add ownership on upgrade.
type RacingInstancePolicy struct {
	ReclaimEnabled     bool   `json:"reclaimEnabled"`
	InstanceID         int    `json:"instanceId"`
	Enabled            bool   `json:"enabled"`
	MaxConcurrentAdds  int    `json:"maxConcurrentAdds"`
	MaxActiveDownloads int    `json:"maxActiveDownloads"`
	MinFreeBytes       int64  `json:"minFreeBytes"`
	SavePath           string `json:"savePath"`
	Category           string `json:"category"`
	AutoTMM            bool   `json:"autoTMM"`
	StartPaused        bool   `json:"startPaused"`
	UpdatedAt          string `json:"updatedAt"`
}

// RacingAddPlan is frozen when an intent is submitted. Transport secrets live
// in separately encrypted metainfo; the public plan can explain the decision.
type RacingAddPlan struct {
	TrackerHosts          []string             `json:"trackerHosts"`
	ConfigurationRevision int64                `json:"configurationRevision"`
	CandidateUpdatedAt    string               `json:"candidateUpdatedAt"`
	CandidateKey          string               `json:"candidateKey"`
	SiteID                int                  `json:"siteId"`
	InstanceID            int                  `json:"instanceId"`
	Rule                  RacingRule           `json:"rule"`
	Policy                RacingInstancePolicy `json:"policy"`
	HashV1                string               `json:"hashV1,omitempty"`
	HashV2                string               `json:"hashV2,omitempty"`
	SizeBytes             int64                `json:"sizeBytes"`
	Options               map[string]string    `json:"options"`
	PoolIDs               []int                `json:"poolIds"`
	Deadline              string               `json:"deadline"`
	FirstSeenAt           string               `json:"firstSeenAt"`
}

type RacingAddIntent struct {
	CandidateKey  string        `json:"candidateKey"`
	InstanceID    int           `json:"instanceId"`
	Plan          RacingAddPlan `json:"plan"`
	State         string        `json:"state"`
	Reason        string        `json:"reason"`
	ReservedAt    string        `json:"reservedAt"`
	SubmittedAt   *string       `json:"submittedAt,omitempty"`
	AcceptedAt    *string       `json:"acceptedAt,omitempty"`
	ConfirmedAt   *string       `json:"confirmedAt,omitempty"`
	RunnableAt    *string       `json:"runnableAt,omitempty"`
	TransferredAt *string       `json:"transferredAt,omitempty"`
	ObservedAt    *string       `json:"observedAt,omitempty"`
	UpdatedAt     string        `json:"updatedAt"`
}

type RacingSpaceCommitment struct {
	CandidateKey  string `json:"candidateKey"`
	StoragePoolID int    `json:"storagePoolId"`
	Bytes         int64  `json:"bytes"`
}

// AvailableBytes already excludes the remaining writes of existing torrents.
// CoveredCandidates identify confirmed intents represented in that same snapshot;
// their original reservations must not be subtracted a second time.
type RacingPoolBudget struct {
	StoragePoolID     int
	AvailableBytes    int64
	ObservedAt        time.Time
	CoveredCandidates []string
}

type RacingReservation struct {
	Plan            RacingAddPlan
	Metainfo        []byte
	Pools           []RacingPoolBudget
	ActiveDownloads int
	ObservedAt      time.Time
}
