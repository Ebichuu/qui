// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/autobrr/go-torrent/metainfo"
	"github.com/rs/zerolog/log"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/qbittorrent"
	"github.com/autobrr/qui/internal/services/racing/sources"
)

type ExecutionReader interface {
	CachedExecutionObservations() []qbittorrent.ExecutionObservation
}
type ExecutionClient interface {
	AddTorrentOnce(context.Context, int, []byte, map[string]string) (*qbt.TorrentAddResponse, error)
	GetTorrentTrackers(context.Context, int, string) ([]qbt.TorrentTracker, error)
}
type executionRunner struct {
	service   *Service
	store     *models.RacingStore
	mu        sync.Mutex
	busy      map[string]bool
	slots     map[int]int
	attempted map[string]time.Time
	workers   sync.WaitGroup
}

func newExecutionRunner(store *models.RacingStore, service *Service) *executionRunner {
	return &executionRunner{service: service, store: store, busy: map[string]bool{}, slots: map[int]int{}, attempted: map[string]time.Time{}}
}

func (s *Service) SetExecutionClients(reader ExecutionReader, client ExecutionClient) {
	s.mu.Lock()
	s.executionReader = reader
	s.executionClient = client
	s.mu.Unlock()
}

func (e *executionRunner) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer e.workers.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.tick(ctx)
		}
	}
}

func (e *executionRunner) allIntents(ctx context.Context) ([]models.RacingAddIntent, error) {
	result := []models.RacingAddIntent{}
	after := ""
	for {
		items, err := e.store.AddIntents(ctx, after, 100)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if len(items) < 100 {
			return result, nil
		}
		after = items[len(items)-1].CandidateKey
	}
}

func (e *executionRunner) tick(ctx context.Context) {
	e.service.mu.RLock()
	config, reader, client, ready := e.service.configuration, e.service.executionReader, e.service.executionClient, e.service.status.ConfigurationReady
	e.service.mu.RUnlock()
	if config == nil || reader == nil || client == nil || !ready {
		return
	}
	enabled := slices.ContainsFunc(config.ExecutionPolicies, func(p models.RacingInstancePolicy) bool { return p.Enabled })
	intents, err := e.allIntents(ctx)
	if err != nil {
		return
	}
	observations := reader.CachedExecutionObservations()
	for _, intent := range intents {
		if intent.State == "confirmed" {
			e.observeConfirmed(ctx, intent, observations)
			continue
		}
		if intent.State == "cancelled" || intent.State == "retired" {
			continue
		}
		// A reserved plan can be discarded; submitted work keeps its original target
		// even after the reception switch, sources or rules have been disabled.
		deadline, _ := time.Parse(time.RFC3339Nano, intent.Plan.Deadline)
		if intent.State == "reserved" && (intent.Plan.ConfigurationRevision != config.Revision || !time.Now().Before(deadline)) {
			_ = e.store.CancelReserved(ctx, intent)
			continue
		}
		e.launch(ctx, intent, reader, client)
	}
	if !enabled {
		return
	}
	budgets := executionBudgets(config, observations, intents, time.Now())
	existing := make(map[string]bool, len(intents))
	for _, intent := range intents {
		if intent.State != "cancelled" {
			existing[intent.CandidateKey] = true
		}
	}
	offset := 0
	for ctx.Err() == nil {
		records, err := e.store.ExecutableCandidates(ctx, offset, 100)
		if err != nil || len(records) == 0 {
			return
		}
		offset += len(records)
		for _, record := range records {
			if record.State != "ready" || existing[record.Key] {
				continue
			}
			var candidate Candidate
			if json.Unmarshal(record.Candidate, &candidate) != nil {
				continue
			}
			selection := SelectRule(candidate, config, time.Now(), nil)
			if selection.State != "ready" || selection.Rule == nil || selection.Deadline == nil {
				continue
			}
			// Only the winning target's explicit opt-in can trigger metainfo fetching.
			optedIn := false
			for _, policy := range config.ExecutionPolicies {
				if !policy.Enabled {
					continue
				}
				if selection.Rule.TargetInstanceID != nil && *selection.Rule.TargetInstanceID == policy.InstanceID {
					optedIn = true
				}
				if selection.Rule.TargetGroupID != nil {
					for _, group := range config.Groups {
						if group.ID == *selection.Rule.TargetGroupID && group.Enabled && slices.Contains(group.InstanceIDs, policy.InstanceID) {
							optedIn = true
						}
					}
				}
			}
			if !optedIn {
				continue
			}
			if candidate.VerifiedMetadata == nil {
				e.requestMetainfo(ctx, record)
				continue
			}
			proof := candidate.VerifiedMetadata
			targets := receptionTargets(*selection.Rule, config, config.ExecutionPolicies, observations, intents, budgets, proof.SizeBytes, time.Now())
			targets = avoidDuplicateParticipants(targets, observations, proof.HashV1, proof.HashV2)
			if len(targets) == 0 {
				continue
			}
			raw, err := e.store.CandidateMetainfo(ctx, record.Key)
			if err != nil {
				continue
			}
			actual, err := sources.ParseMetainfo(raw)
			if err != nil || actual != *proof {
				continue
			}
			var hosts []string
			for _, site := range config.Sites {
				if site.ID == candidate.SiteID {
					hosts = site.TrackerHosts
					break
				}
			}
			if len(expectedTrackers(raw, hosts)) == 0 {
				continue
			}
			for _, target := range targets {
				deadline := *selection.Deadline
				if candidate.Item.FreeExpiresAt != nil && candidate.Item.FreeExpiresAt.After(time.Now()) && candidate.Item.FreeExpiresAt.Before(deadline) {
					deadline = *candidate.Item.FreeExpiresAt
				}
				plan := models.RacingAddPlan{CandidateKey: record.Key, SiteID: record.SiteID, InstanceID: target.instance.InstanceID, Rule: *selection.Rule, Policy: target.policy, HashV1: proof.HashV1, HashV2: proof.HashV2, SizeBytes: proof.SizeBytes, Options: target.options, PoolIDs: target.pools, Deadline: deadline.UTC().Format(time.RFC3339Nano), FirstSeenAt: record.FirstSeenAt, ConfigurationRevision: config.Revision, CandidateUpdatedAt: record.UpdatedAt, TrackerHosts: hosts}
				err := e.store.ReserveAdd(ctx, models.RacingReservation{Plan: plan, Metainfo: raw, Pools: budgets, ActiveDownloads: target.active, ObservedAt: *target.instance.ObservedAt})
				if err != nil {
					if errors.Is(err, models.ErrRacingCapacity) {
						continue
					}
					break
				}
				intent, err := e.store.AddIntent(ctx, record.Key)
				if err != nil {
					break
				}
				intents = append(intents, intent)
				existing[record.Key] = true
				e.launch(ctx, intent, reader, client)
				break
			}
		}
		if len(records) < 100 {
			return
		}
	}
}

func (e *executionRunner) requestMetainfo(ctx context.Context, record models.RacingCandidateRecord) {
	if e.service.metadata == nil {
		return
	}
	input, err := e.store.EventDiscoveries(ctx, record.Key, record.SiteID, record.EventKey, record.SourceScope)
	if err == nil {
		e.service.metadata.enqueue(metadataJob{key: record.Key, siteID: record.SiteID, event: record.EventKey, scope: record.SourceScope, rows: input.Observations})
	}
}

// Confirmed tasks only read the shared snapshot. They cannot occupy network
// execution slots needed to submit and confirm newly admitted candidates.
func (e *executionRunner) observeConfirmed(ctx context.Context, intent models.RacingAddIntent, observations []qbittorrent.ExecutionObservation) {
	e.mu.Lock()
	if e.busy[intent.CandidateKey] || time.Since(e.attempted[intent.CandidateKey]) < 5*time.Second {
		e.mu.Unlock()
		return
	}
	e.attempted[intent.CandidateKey] = time.Now()
	e.mu.Unlock()
	instance, found := executionInstance(observations, intent.InstanceID)
	if !found || !freshExecution(instance, time.Now()) {
		return
	}
	torrent, present := intentTorrent(instance, intent)
	if !present {
		_ = e.store.ObserveConfirmedMissing(ctx, intent, *instance.ObservedAt)
		return
	}
	_ = e.store.ConfirmAdd(ctx, intent.CandidateKey, *instance.ObservedAt, torrentRunnable(torrent.State), torrent.DownloadSpeed > 0 || torrent.UploadSpeed > 0 || torrent.Downloaded > 0 || torrent.Uploaded > 0)
}

func (e *executionRunner) launch(ctx context.Context, intent models.RacingAddIntent, reader ExecutionReader, client ExecutionClient) {
	e.mu.Lock()
	retry := 5 * time.Second
	if intent.State == "accepted" || intent.State == "submitted" {
		retry = time.Second
	}

	if e.busy[intent.CandidateKey] || time.Since(e.attempted[intent.CandidateKey]) < retry || e.slots[intent.InstanceID] >= max(1, intent.Plan.Policy.MaxConcurrentAdds) {
		e.mu.Unlock()
		return
	}
	e.busy[intent.CandidateKey] = true
	e.slots[intent.InstanceID]++
	e.mu.Unlock()
	e.workers.Go(func() {
		defer func() {
			e.mu.Lock()
			delete(e.busy, intent.CandidateKey)
			e.slots[intent.InstanceID]--
			e.attempted[intent.CandidateKey] = time.Now()
			for key, at := range e.attempted {
				if time.Since(at) > 5*time.Minute {
					delete(e.attempted, key)
				}
			}
			e.mu.Unlock()
		}()
		e.execute(ctx, intent, reader, client)
	})
}

func (e *executionRunner) execute(parent context.Context, intent models.RacingAddIntent, reader ExecutionReader, client ExecutionClient) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	raw, err := e.store.IntentMetainfo(ctx, intent.CandidateKey)
	if err != nil {
		_ = e.store.RecordIntentReason(ctx, intent.CandidateKey, "metainfo_unavailable")
		return
	}
	proof, err := sources.ParseMetainfo(raw)
	if err != nil || proof.HashV1 != intent.Plan.HashV1 || proof.HashV2 != intent.Plan.HashV2 || proof.SizeBytes != intent.Plan.SizeBytes {
		_ = e.store.RecordIntentReason(ctx, intent.CandidateKey, "metainfo_unavailable")
		return
	}
	expected := expectedTrackers(raw, intent.Plan.TrackerHosts)
	if len(expected) == 0 {
		_ = e.store.RecordIntentReason(ctx, intent.CandidateKey, "tracker_identity_unverified")
		return
	}
	observations := reader.CachedExecutionObservations()
	instance, found := executionInstance(observations, intent.InstanceID)
	if !found || !freshExecution(instance, time.Now()) {
		_ = e.store.RecordIntentReason(ctx, intent.CandidateKey, "target_unavailable")
		return
	}
	torrent, present := intentTorrent(instance, intent)
	if intent.State != "reserved" {
		if !present {
			if intent.State == "confirmed" {
				_ = e.store.ObserveConfirmedMissing(ctx, intent, *instance.ObservedAt)
			} else {
				_ = e.store.RecordIntentReason(ctx, intent.CandidateKey, "awaiting_task")
			}
			return
		}
		if intent.State == "confirmed" {
			_ = e.store.ConfirmAdd(ctx, intent.CandidateKey, *instance.ObservedAt, torrentRunnable(torrent.State), torrent.DownloadSpeed > 0 || torrent.UploadSpeed > 0 || torrent.Downloaded > 0 || torrent.Uploaded > 0)
			return
		}
		e.confirm(ctx, intent, instance, torrent, expected, client)
		return
	}
	if present {
		trackers, err := client.GetTorrentTrackers(ctx, intent.InstanceID, torrent.Hash)
		if err != nil || !hasExpectedTracker(trackers, expected) {
			_ = e.store.RecordIntentReason(ctx, intent.CandidateKey, "existing_tracker_mismatch")
			return
		}
	}
	// Re-read shared snapshots after any preflight network wait. Never retain a
	// database transaction while looking up trackers or sending the add request.
	observations = reader.CachedExecutionObservations()
	instance, found = executionInstance(observations, intent.InstanceID)
	if !found || !freshExecution(instance, time.Now()) {
		return
	}
	e.service.mu.RLock()
	config := e.service.configuration
	e.service.mu.RUnlock()
	if config == nil {
		return
	}
	target, layoutOK := makeReceptionTarget(config, instance, intent.Plan.Policy)
	if !layoutOK || !slices.Equal(target.pools, intent.Plan.PoolIDs) || !reflect.DeepEqual(target.options, intent.Plan.Options) {
		_ = e.store.CancelReserved(ctx, intent)
		return
	}
	torrent, present = intentTorrent(instance, intent)
	intents, err := e.allIntents(ctx)
	if err != nil {
		return
	}
	budgets := executionBudgets(config, observations, intents, time.Now())
	active := 0
	for _, item := range instance.Torrents {
		if item.Remaining != 0 {
			active++
		}
	}
	if err := e.store.SubmitAdd(ctx, intent, budgets, active, *instance.ObservedAt); err != nil {
		if errors.Is(err, models.ErrRacingStale) {
			_ = e.store.CancelReserved(ctx, intent)
		}
		return
	}
	if present {
		e.confirm(ctx, intent, instance, torrent, expected, client)
		return
	}
	// From this point even cancellation or a malformed response is an unknown
	// result. Recovery only observes this target; it never calls AddTorrent again.
	response, err := client.AddTorrentOnce(ctx, intent.InstanceID, raw, intent.Plan.Options)
	accepted := err == nil && (response == nil || response.FailureCount == 0)
	persistCtx, persistCancel := context.WithTimeout(parent, 5*time.Second)
	defer persistCancel()
	_ = e.store.RecordAddResult(persistCtx, intent.CandidateKey, accepted)
	log.Debug().Str("component", "racing").Str("candidate_key", intent.CandidateKey).Int("instance_id", intent.InstanceID).Bool("accepted", accepted).Msg("Torrent add request completed; waiting for identity confirmation")
}

func (e *executionRunner) confirm(ctx context.Context, intent models.RacingAddIntent, instance qbittorrent.ExecutionObservation, torrent qbittorrent.ExecutionTorrent, expected []string, client ExecutionClient) {
	trackers, err := client.GetTorrentTrackers(ctx, intent.InstanceID, torrent.Hash)
	if err != nil || !hasExpectedTracker(trackers, expected) {
		_ = e.store.RecordIntentReason(ctx, intent.CandidateKey, "tracker_identity_unverified")
		return
	}
	_ = e.store.ConfirmAdd(ctx, intent.CandidateKey, *instance.ObservedAt, torrentRunnable(torrent.State), torrent.DownloadSpeed > 0 || torrent.UploadSpeed > 0 || torrent.Downloaded > 0 || torrent.Uploaded > 0)
}

func executionInstance(observations []qbittorrent.ExecutionObservation, id int) (qbittorrent.ExecutionObservation, bool) {
	for _, instance := range observations {
		if instance.InstanceID == id {
			return instance, true
		}
	}
	return qbittorrent.ExecutionObservation{}, false
}
func intentTorrent(instance qbittorrent.ExecutionObservation, intent models.RacingAddIntent) (qbittorrent.ExecutionTorrent, bool) {
	for _, torrent := range instance.Torrents {
		if matchesIntentTorrent(intent, torrent) {
			return torrent, true
		}
	}
	return qbittorrent.ExecutionTorrent{}, false
}
func expectedTrackers(raw []byte, hosts []string) []string {
	meta, err := metainfo.Load(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	result := []string{}
	for _, tracker := range meta.UpvertedAnnounceList().DistinctValues() {
		parsed, err := url.Parse(tracker)
		if err != nil {
			continue
		}
		if (parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "udp") && slices.Contains(hosts, strings.ToLower(parsed.Hostname())) {
			result = append(result, tracker)
		}
	}
	return result
}
func hasExpectedTracker(trackers []qbt.TorrentTracker, expected []string) bool {
	for _, tracker := range trackers {
		if slices.Contains(expected, tracker.Url) {
			return true
		}
	}
	return false
}
