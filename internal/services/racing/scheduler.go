// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/qbittorrent"
)

type receptionTarget struct {
	instance     qbittorrent.ExecutionObservation
	policy       models.RacingInstancePolicy
	pools        []int
	options      map[string]string
	active       int
	pending      int
	available    int64
	lastReserved string
}

func freshExecution(instance qbittorrent.ExecutionObservation, now time.Time) bool {
	return instance.Healthy && instance.Fresh && instance.PreferencesFresh && instance.ObservedAt != nil && instance.PreferencesObservedAt != nil && !instance.ObservedAt.Before(*instance.PreferencesObservedAt) && !now.Before(*instance.ObservedAt) && now.Sub(*instance.ObservedAt) <= 5*time.Second
}

// executionBudgets accounts for every mapped instance, regardless of its groups
// or reception switch. Existing paused/queued torrents still promise future
// disk writes. Unknown members only block the disks to which they are mapped.
func executionBudgets(config *models.RacingConfiguration, observations []qbittorrent.ExecutionObservation, intents []models.RacingAddIntent, now time.Time) []models.RacingPoolBudget {
	type poolState struct {
		budget         models.RacingPoolBudget
		freeKnown      bool
		unknown        bool
		remaining      int64
		latestState    time.Time
		latestCapacity time.Time
	}
	pools := make(map[int]*poolState, len(config.StoragePools))
	for _, pool := range config.StoragePools {
		pools[pool.ID] = &poolState{budget: models.RacingPoolBudget{StoragePoolID: pool.ID}}
	}
	instances := make(map[int]qbittorrent.ExecutionObservation, len(observations))
	for _, instance := range observations {
		instances[instance.InstanceID] = instance
	}
	members := make(map[int]map[int]bool)
	for _, mapping := range config.PathMappings {
		if members[mapping.InstanceID] == nil {
			members[mapping.InstanceID] = make(map[int]bool)
		}
		members[mapping.InstanceID][mapping.StoragePoolID] = true
	}
	byHash := make(map[int]map[string][]models.RacingAddIntent)
	for _, intent := range intents {
		if intent.State != "confirmed" {
			continue
		}
		if byHash[intent.InstanceID] == nil {
			byHash[intent.InstanceID] = make(map[string][]models.RacingAddIntent)
		}
		for _, hash := range []string{intent.Plan.HashV1, intent.Plan.HashV2} {
			if hash != "" {
				byHash[intent.InstanceID][strings.ToLower(hash)] = append(byHash[intent.InstanceID][strings.ToLower(hash)], intent)
			}
		}
	}
	for id, mappedPools := range members {
		instance, present := instances[id]
		block := func() {
			for poolID := range mappedPools {
				if pool := pools[poolID]; pool != nil {
					pool.unknown = true
				}
			}
		}
		if !present || !freshExecution(instance, now) {
			block()
			continue
		}
		for poolID := range mappedPools {
			if pool := pools[poolID]; pool != nil && instance.ObservedAt.After(pool.latestState) {
				pool.latestState = *instance.ObservedAt
			}
			if pool := pools[poolID]; pool != nil && (pool.budget.ObservedAt.IsZero() || instance.ObservedAt.Before(pool.budget.ObservedAt)) {
				pool.budget.ObservedAt = *instance.ObservedAt
			}
		}
		if id, ok := ResolveStoragePool(config.PathMappings, id, instance.SavePath); ok {
			if pool := pools[id]; pool != nil && instance.DefaultPathFreeBytes != nil && *instance.DefaultPathFreeBytes >= 0 {
				if !pool.freeKnown || *instance.DefaultPathFreeBytes < pool.budget.AvailableBytes {
					pool.budget.AvailableBytes = *instance.DefaultPathFreeBytes
				}
				pool.freeKnown = true
				if instance.ObservedAt.After(pool.latestCapacity) {
					pool.latestCapacity = *instance.ObservedAt
				}
			}
		}
		for _, torrent := range instance.Torrents {
			writes, known := torrentFutureWrites(config.PathMappings, instance, torrent)
			if !known {
				block()
				continue
			}
			for poolID, size := range writes {
				if pool := pools[poolID]; pool != nil {
					if size < 0 || size > math.MaxInt64-pool.remaining {
						pool.unknown = true
					} else {
						pool.remaining += size
					}
				}
			}
			for _, hash := range []string{torrent.Hash, torrent.HashV1, torrent.HashV2} {
				if hash == "" {
					continue
				}
				for _, intent := range byHash[id][strings.ToLower(hash)] {
					// A fully resolved current layout also proves that an old temporary pool
					// has no remaining promise after completion or an external move.
					for _, poolID := range intent.Plan.PoolIDs {
						if pool := pools[poolID]; pool != nil {
							pool.budget.CoveredCandidates = append(pool.budget.CoveredCandidates, intent.CandidateKey)
						}
					}
				}
			}
		}
	}
	result := make([]models.RacingPoolBudget, 0, len(pools))
	for _, pool := range pools {
		if pool.unknown || !pool.freeKnown || pool.budget.ObservedAt.IsZero() || pool.latestCapacity.Before(pool.latestState) {
			continue
		}
		pool.budget.AvailableBytes -= pool.remaining
		result = append(result, pool.budget)
	}
	slices.SortFunc(result, func(a, b models.RacingPoolBudget) int { return a.StoragePoolID - b.StoragePoolID })
	return result
}

func torrentFutureWrites(mappings []models.RacingPathMapping, instance qbittorrent.ExecutionObservation, torrent qbittorrent.ExecutionTorrent) (map[int]int64, bool) {
	result := map[int]int64{}
	if torrent.Remaining < 0 {
		return result, false
	}
	final, ok := ResolveStoragePool(mappings, instance.InstanceID, torrent.SavePath)
	if !ok {
		return result, false
	}
	if torrent.Remaining == 0 {
		result[final] = 0
		return result, true
	}
	tempPath := torrent.DownloadPath
	if tempPath == "" && instance.TempPathEnabled {
		tempPath = instance.TempPath
	}
	temp := final
	if tempPath != "" {
		temp, ok = ResolveStoragePool(mappings, instance.InstanceID, tempPath)
		if !ok {
			return result, false
		}
	}
	result[temp] = torrent.Remaining
	if temp == final {
		return result, true
	}
	// A move from another disk must still write the complete payload to the final
	// disk, including bytes already downloaded into the temporary directory.
	result[final] = max(torrent.Size, torrent.Remaining)
	return result, true
}

func matchesIntentTorrent(intent models.RacingAddIntent, torrent qbittorrent.ExecutionTorrent) bool {
	for _, expected := range []string{intent.Plan.HashV1, intent.Plan.HashV2} {
		if expected == "" {
			continue
		}
		for _, actual := range []string{torrent.Hash, torrent.HashV1, torrent.HashV2} {
			if strings.EqualFold(expected, actual) {
				return true
			}
		}
	}
	return false
}

func receptionTargets(rule models.RacingRule, config *models.RacingConfiguration, policies []models.RacingInstancePolicy, observations []qbittorrent.ExecutionObservation, intents []models.RacingAddIntent, budgets []models.RacingPoolBudget, size int64, now time.Time) []receptionTarget {
	return receptionTargetsWithCapacity(rule, config, policies, observations, intents, budgets, size, now, false)
}

func receptionTargetsWithCapacity(rule models.RacingRule, config *models.RacingConfiguration, policies []models.RacingInstancePolicy, observations []qbittorrent.ExecutionObservation, intents []models.RacingAddIntent, budgets []models.RacingPoolBudget, size int64, now time.Time, allowDeficit bool) []receptionTarget {
	allowed := []int{}
	if rule.TargetInstanceID != nil {
		allowed = append(allowed, *rule.TargetInstanceID)
	}
	if rule.TargetGroupID != nil {
		for _, group := range config.Groups {
			if group.ID == *rule.TargetGroupID && group.Enabled {
				allowed = group.InstanceIDs
				break
			}
		}
	}
	result := []receptionTarget{}
	for _, instance := range observations {
		if !slices.Contains(allowed, instance.InstanceID) || !freshExecution(instance, now) {
			continue
		}
		index := slices.IndexFunc(policies, func(p models.RacingInstancePolicy) bool { return p.InstanceID == instance.InstanceID && p.Enabled })
		if index < 0 {
			continue
		}
		policy := policies[index]
		target, ok := makeReceptionTarget(config, instance, policy)
		if !ok {
			continue
		}
		for _, torrent := range instance.Torrents {
			if torrent.Remaining != 0 {
				target.active++
			}
		}
		for _, intent := range intents {
			if intent.InstanceID == instance.InstanceID && intent.State != "cancelled" {
				if intent.State != "confirmed" && intent.State != "retired" {
					target.pending++
				}
				if intent.ReservedAt > target.lastReserved {
					target.lastReserved = intent.ReservedAt
				}
			}
		}
		if target.pending >= policy.MaxConcurrentAdds || target.active+target.pending >= policy.MaxActiveDownloads {
			continue
		}
		target.available = math.MaxInt64
		for _, poolID := range target.pools {
			index := slices.IndexFunc(budgets, func(b models.RacingPoolBudget) bool { return b.StoragePoolID == poolID })
			if index < 0 {
				ok = false
				break
			}
			budget := budgets[index]
			available := budget.AvailableBytes
			for _, intent := range intents {
				if intent.State == "cancelled" || intent.State == "retired" || !slices.Contains(intent.Plan.PoolIDs, poolID) {
					continue
				}
				covered := false
				if intent.State == "confirmed" && intent.ConfirmedAt != nil && slices.Contains(budget.CoveredCandidates, intent.CandidateKey) {
					at, err := time.Parse(time.RFC3339Nano, *intent.ConfirmedAt)
					covered = err == nil && !at.After(budget.ObservedAt)
				}
				if !covered {
					if intent.Plan.SizeBytes < 0 || available < math.MinInt64+intent.Plan.SizeBytes {
						ok = false
						break
					}
					available -= intent.Plan.SizeBytes
				}
			}
			if !ok || available < math.MinInt64+policy.MinFreeBytes || (!allowDeficit && (policy.MinFreeBytes > available || size > available-policy.MinFreeBytes)) {
				ok = false
				break
			}
			target.available = min(target.available, available-policy.MinFreeBytes)
		}
		if ok {
			result = append(result, target)
		}
	}
	// Use one fixed reference for the whole sort, rather than pairwise
	// tolerances (which would make the comparator non-transitive).
	minimumLoad := int64(math.MaxInt64)
	for _, target := range result {
		minimumLoad = min(minimumLoad, receptionLoad(target))
	}
	tolerance := max(int64(1<<20), minimumLoad/10)
	sort.SliceStable(result, func(i, j int) bool {
		a, b := result[i], result[j]
		aLoad, bLoad := receptionLoad(a), receptionLoad(b)
		aNear, bNear := aLoad-minimumLoad <= tolerance, bLoad-minimumLoad <= tolerance
		if aNear != bNear {
			return aNear
		}
		if !aNear && aLoad != bLoad {
			return aLoad < bLoad
		}
		if a.active+a.pending != b.active+b.pending {
			return a.active+a.pending < b.active+b.pending
		}
		// Capacity is already a hard eligibility constraint. Among similarly
		// loaded eligible instances, history must beat minor capacity noise.
		if a.lastReserved != b.lastReserved {
			return a.lastReserved < b.lastReserved
		}
		if aLoad != bLoad {
			return aLoad < bLoad
		}
		if a.available != b.available {
			return a.available > b.available
		}
		return a.instance.InstanceID < b.instance.InstanceID
	})
	return result
}

func receptionLoad(target receptionTarget) int64 {
	return max(int64(0), *target.instance.DownloadSpeed, *target.instance.UploadSpeed)
}

func makeReceptionTarget(config *models.RacingConfiguration, instance qbittorrent.ExecutionObservation, policy models.RacingInstancePolicy) (receptionTarget, bool) {
	target := receptionTarget{instance: instance, policy: policy}
	if instance.DownloadSpeed == nil || instance.UploadSpeed == nil {
		return target, false
	}
	savePath := policy.SavePath
	if savePath == "" {
		savePath = instance.SavePath
	}
	if policy.Category != "" {
		category, found := instance.Categories[policy.Category]
		if !found {
			return target, false
		}
		if policy.AutoTMM && category.SavePath != "" {
			savePath = category.SavePath
		}
	}
	final, ok := ResolveStoragePool(config.PathMappings, instance.InstanceID, savePath)
	if !ok {
		return target, false
	}
	target.pools = []int{final}
	if instance.TempPathEnabled {
		temp, ok := ResolveStoragePool(config.PathMappings, instance.InstanceID, instance.TempPath)
		if !ok {
			return target, false
		}
		if temp != final {
			target.pools = append(target.pools, temp)
		}
	}
	target.options = map[string]string{"autoTMM": strconv.FormatBool(policy.AutoTMM), "category": policy.Category, "paused": strconv.FormatBool(policy.StartPaused), "stopped": strconv.FormatBool(policy.StartPaused)}
	if !policy.AutoTMM {
		target.options["savepath"] = savePath
	}
	// Explicitly retain the observed temporary layout; a new request cannot
	// inherit an unobserved global preference change at send time.
	target.options["useDownloadPath"] = strconv.FormatBool(instance.TempPathEnabled)
	if instance.TempPathEnabled {
		target.options["downloadPath"] = instance.TempPath
	}
	return target, true
}

func torrentRunnable(state qbt.TorrentState) bool {
	switch state {
	case qbt.TorrentStateDownloading, qbt.TorrentStateForcedDl, qbt.TorrentStateStalledDl, qbt.TorrentStateUploading, qbt.TorrentStateForcedUp, qbt.TorrentStateStalledUp:
		return true
	default:
		return false
	}
}

// Cross-instance participation needs an explicit policy, which C08 does not
// offer. If content already exists, only that instance may be confirmed; a
// matching rule cannot accidentally create another copy on a different target.
func avoidDuplicateParticipants(targets []receptionTarget, observations []qbittorrent.ExecutionObservation, hashV1, hashV2 string) []receptionTarget {
	probe := models.RacingAddIntent{Plan: models.RacingAddPlan{HashV1: hashV1, HashV2: hashV2}}
	existing := map[int]bool{}
	for _, instance := range observations {
		for _, torrent := range instance.Torrents {
			if matchesIntentTorrent(probe, torrent) {
				existing[instance.InstanceID] = true
				break
			}
		}
	}
	if len(existing) == 0 {
		return targets
	}
	return slices.DeleteFunc(targets, func(target receptionTarget) bool { return !existing[target.instance.InstanceID] })
}
