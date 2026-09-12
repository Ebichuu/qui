// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"cmp"
	"math"
	"math/bits"
	"slices"
	"time"

	"github.com/autobrr/qui/internal/models"
)

// ReclaimEvidence contains verified effective capacity, not torrent logical size.
// Physical release and revoked existing write promises are counted separately.
type ReclaimEvidence struct {
	Hash                  string        `json:"hash"`
	AddedOn               int64         `json:"addedOn"`
	InstanceID            int           `json:"instanceID"`
	PoolID                int           `json:"poolID"`
	PhysicalBytes         int64         `json:"physicalBytes"`
	FutureWriteBytes      int64         `json:"futureWriteBytes"`
	RecentUploadBytes     int64         `json:"recentUploadBytes"`
	LowEfficiencyDuration time.Duration `json:"lowEfficiencyDuration"`
	ObservedAt            time.Time     `json:"observedAt"`
	CapacityKnown         bool          `json:"capacityKnown"`
	UploadWindowCovered   bool          `json:"uploadWindowCovered"`
	Protected             bool          `json:"protected"`
	Owned                 bool          `json:"owned"`
}

type ReclaimSpent struct {
	Deletes           int
	CapacityBytes     int64
	RecentUploadBytes int64
	OvershootBytes    int64
}

type ReclaimAssessment struct {
	State                     string            `json:"state"`
	DeficitBytes              int64             `json:"deficitBytes"`
	PhysicalBytes             int64             `json:"physicalBytes"`
	FutureWriteBytes          int64             `json:"futureWriteBytes"`
	RecentUploadBytes         int64             `json:"recentUploadBytes"`
	ObservedCandidates        int               `json:"observedCandidates"`
	UnknownCapacityCandidates int               `json:"unknownCapacityCandidates"`
	UnknownUploadCandidates   int               `json:"unknownUploadCandidates"`
	Selected                  []ReclaimEvidence `json:"selected"`
}

// reclaimDeficit preserves a negative available balance: old overcommitment is
// part of the new deficit, rather than silently clamping available space to zero.
func reclaimDeficit(required, available int64) (int64, bool) {
	if required <= 0 {
		return 0, false
	}
	if available >= required {
		return 0, true
	}
	if available < 0 && required > math.MaxInt64+available {
		return 0, false
	}
	return required - available, true
}

// assessReclaim never claims candidates or sends actions. The first plan's
// ceilings remain binding even if current settings are raised; lower settings
// constrain every unsubmitted step, including after previous partial execution.
func assessReclaim(instanceID, poolID int, required, available int64, frozen, current models.RacingReclaimPolicy, spent ReclaimSpent, evidence []ReclaimEvidence, now time.Time) ReclaimAssessment {
	result := ReclaimAssessment{State: "unavailable", Selected: []ReclaimEvidence{}, ObservedCandidates: len(evidence)}
	for _, item := range evidence {
		if !item.CapacityKnown {
			result.UnknownCapacityCandidates++
		}
		if !item.UploadWindowCovered {
			result.UnknownUploadCandidates++
		}
	}
	for _, policy := range []models.RacingReclaimPolicy{frozen, current} {
		if policy.MaxDeletes < 0 || policy.MaxReclaimBytes < 0 || policy.MaxRecentUploadBytes < 0 || policy.MaxOvershootBytes < 0 {
			return result
		}
	}
	if frozen.RecentUploadWindowSeconds != current.RecentUploadWindowSeconds {
		result.State = "upload_window_changed"
		return result
	}
	deficit, valid := reclaimDeficit(required, available)
	if !valid || spent.Deletes < 0 || spent.CapacityBytes < 0 || spent.RecentUploadBytes < 0 || spent.OvershootBytes < 0 {
		return result
	}
	result.DeficitBytes = deficit
	if deficit == 0 {
		result.State = "space_sufficient"
		return result
	}
	if !frozen.Enabled || !current.Enabled {
		result.State = "disabled"
		return result
	}
	maxDeletes := min(frozen.MaxDeletes, current.MaxDeletes) - spent.Deletes
	maxCapacity := min(frozen.MaxReclaimBytes, current.MaxReclaimBytes) - spent.CapacityBytes
	maxUpload := min(frozen.MaxRecentUploadBytes, current.MaxRecentUploadBytes) - spent.RecentUploadBytes
	maxOvershoot := min(frozen.MaxOvershootBytes, current.MaxOvershootBytes) - spent.OvershootBytes
	if maxDeletes <= 0 || maxCapacity < deficit || maxUpload < 0 || maxOvershoot < 0 {
		result.State = "budget_exhausted"
		return result
	}
	candidates := []ReclaimEvidence{}
	seen := map[string]bool{}
	for _, item := range evidence {
		if item.InstanceID != instanceID || item.PoolID != poolID || item.Hash == "" || item.AddedOn <= 0 || item.Protected || item.Owned || !item.CapacityKnown || !item.UploadWindowCovered || item.PhysicalBytes < 0 || item.FutureWriteBytes < 0 || item.RecentUploadBytes < 0 || item.LowEfficiencyDuration <= 0 || item.ObservedAt.IsZero() || now.Before(item.ObservedAt) || now.Sub(item.ObservedAt) > 5*time.Second {
			continue
		}
		if item.PhysicalBytes > math.MaxInt64-item.FutureWriteBytes || item.PhysicalBytes+item.FutureWriteBytes <= 0 {
			continue
		}
		if seen[item.Hash] {
			continue
		}
		seen[item.Hash] = true
		candidates = append(candidates, item)
	}
	slices.SortFunc(candidates, func(a, b ReclaimEvidence) int {
		// Compare upload/capacity exactly without floating point or multiplication overflow.
		ah, al := bits.Mul64(uint64(a.RecentUploadBytes), uint64(b.PhysicalBytes+b.FutureWriteBytes))
		bh, bl := bits.Mul64(uint64(b.RecentUploadBytes), uint64(a.PhysicalBytes+a.FutureWriteBytes))
		if c := cmp.Compare(ah, bh); c != 0 {
			return c
		}
		if c := cmp.Compare(al, bl); c != 0 {
			return c
		}
		if c := cmp.Compare(b.LowEfficiencyDuration, a.LowEfficiencyDuration); c != 0 {
			return c
		}
		return cmp.Compare(a.Hash, b.Hash)
	})
	var total, upload int64
	chosen := []ReclaimEvidence{}
	for _, item := range candidates {
		gain := item.PhysicalBytes + item.FutureWriteBytes
		if len(chosen) >= maxDeletes || gain > maxCapacity-total || item.RecentUploadBytes > maxUpload-upload {
			continue
		}
		if total+gain > deficit && total+gain-deficit > maxOvershoot {
			continue
		}
		chosen = append(chosen, item)
		total += gain
		upload += item.RecentUploadBytes
		if total >= deficit {
			break
		}
	}
	if total < deficit {
		result.State = "insufficient_evidence_or_budget"
		return result
	}
	// Remove redundant members without changing the deterministic preference order.
	for i, c := range slices.Backward(chosen) {
		gain := c.PhysicalBytes + c.FutureWriteBytes
		if total-gain >= deficit {
			total -= gain
			upload -= c.RecentUploadBytes
			chosen = slices.Delete(chosen, i, i+1)
		}
	}
	result.State = "assessed"
	result.Selected = chosen
	result.RecentUploadBytes = upload
	for _, item := range chosen {
		result.PhysicalBytes += item.PhysicalBytes
		result.FutureWriteBytes += item.FutureWriteBytes
	}
	return result
}
