// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/racing/sources"
)

type CandidateStore interface {
	InvalidateCandidate(context.Context, string, []byte) error
	PendingDiscoveries(context.Context, int64, int64, int) (models.RacingPendingBatch, error)
	EventDiscoveries(context.Context, string, int, string, int) (models.RacingCandidateInput, error)
	SaveCandidate(context.Context, models.RacingCandidateRecord, []models.RacingDiscovery) error
	CandidateRecords(context.Context, string, *string, int) ([]models.RacingCandidateRecord, error)
}

type candidateLock struct {
	mu   sync.Mutex
	refs int
}

type candidateEvaluator struct {
	store   CandidateStore
	service *Service
	changed chan struct{}
	wake    chan struct{}
	locksMu sync.Mutex
	locks   map[string]*candidateLock
}

func newCandidateEvaluator(store CandidateStore, service *Service) *candidateEvaluator {
	return &candidateEvaluator{store: store, service: service, changed: make(chan struct{}, 1), wake: make(chan struct{}, 1), locks: make(map[string]*candidateLock)}
}

func (e *candidateEvaluator) lock(key string) func() {
	e.locksMu.Lock()
	entry := e.locks[key]
	if entry == nil {
		entry = &candidateLock{}
		e.locks[key] = entry
	}
	entry.refs++
	e.locksMu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		e.locksMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(e.locks, key)
		}
		e.locksMu.Unlock()
	}
}

func candidateScope(row models.RacingDiscovery) int {
	var item struct {
		TorrentID string `json:"torrentId"`
	}
	if json.Unmarshal(row.Item, &item) != nil || item.TorrentID == "" {
		return row.SourceID
	}
	return 0
}

func (e *candidateEvaluator) evaluate(ctx context.Context, siteID int, event string, scope int) error {
	key := "site:" + strconv.Itoa(siteID) + ":" + event
	if scope > 0 {
		key += ":source:" + strconv.Itoa(scope)
	}
	unlock := e.lock(key)
	defer unlock()
	e.service.mu.RLock()
	config := e.service.configuration
	e.service.mu.RUnlock()
	if config == nil {
		return errors.New("racing configuration unavailable")
	}
	input, err := e.store.EventDiscoveries(ctx, key, siteID, event, scope)
	if err != nil {
		return err
	}
	rows := input.Observations
	if len(rows) == 0 {
		selection, _ := json.Marshal(RuleSelection{State: "rejected", Reason: "source_removed", Priority: "unknown", MatchedKinds: []string{}, MissingFields: []string{}})
		return e.store.InvalidateCandidate(ctx, key, selection)
	}
	candidate, err := MergeDiscoveries(rows)
	if err != nil {
		return err
	}
	candidate.Key = key
	if input.FirstSeenAt != "" {
		first, err := time.Parse(time.RFC3339Nano, input.FirstSeenAt)
		if err != nil {
			return err
		}
		if first.Before(candidate.FirstSeenAt) {
			candidate.FirstSeenAt = first
		}
	}
	if e.service.metadata != nil {
		raw, err := e.service.metadata.store.CandidateMetadata(ctx, key)
		if err != nil {
			return err
		}
		if len(raw) > 0 {
			var metadata sources.VerifiedMetadata
			if err := json.Unmarshal(raw, &metadata); err != nil {
				return err
			}
			ApplyVerifiedMetadata(candidate, metadata)
		}
	}
	now := time.Now().UTC()
	selection := SelectRule(*candidate, config, now, func(rule models.RacingRule) bool {
		if rule.TargetInstanceID != nil {
			return *rule.TargetInstanceID > 0
		}
		if rule.TargetGroupID != nil {
			for _, group := range config.Groups {
				if group.ID == *rule.TargetGroupID {
					return group.Enabled && len(group.InstanceIDs) > 0
				}
			}
		}
		return false
	})
	// C06 checks configuration availability. C08 checks live health, capacity and
	// existing operations immediately before reserving or assigning any machine.
	public, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	decision, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	var nextAt *time.Time
	schedule := func(at time.Time) {
		if at.After(now) && (nextAt == nil || at.Before(*nextAt)) {
			nextAt = &at
		}
	}
	if selection.Deadline != nil {
		schedule(*selection.Deadline)
	}
	if candidate.Item.FreeExpiresAt != nil {
		schedule(*candidate.Item.FreeExpiresAt)
	}
	if candidate.Item.PublishedAt != nil && candidate.Item.PublishedAt.After(now) {
		schedule(*candidate.Item.PublishedAt)
	}
	if needsMetainfo(selection, candidate) && e.service.metadata != nil {
		schedule(now.Add(30 * time.Second))
	}
	var next *string
	if nextAt != nil {
		formatted := nextAt.UTC().Format(time.RFC3339Nano)
		next = &formatted
	}
	if err := e.store.SaveCandidate(ctx, models.RacingCandidateRecord{Key: key, SiteID: siteID, EventKey: event, SourceScope: scope, FirstSeenAt: candidate.FirstSeenAt.Format(time.RFC3339Nano), Candidate: public, Selection: decision, State: selection.State, NextEvaluationAt: next}, rows); err != nil {
		return err
	}
	log.Debug().Str("component", "racing").Str("candidate_key", key).Str("state", selection.State).Str("reason", selection.Reason).Strs("missing", selection.MissingFields).Msg("Candidate rules evaluated")
	if needsMetainfo(selection, candidate) && e.service.metadata != nil {
		e.service.metadata.enqueue(metadataJob{key: key, siteID: siteID, event: event, scope: scope, rows: rows})
	}
	return nil
}

func (e *candidateEvaluator) observed(ctx context.Context, input models.RacingRuntimeSource, event string, item []byte) error {
	scope := candidateScope(models.RacingDiscovery{SourceID: input.ID, Item: item})
	err := e.evaluate(ctx, input.SiteID, event, scope)
	if err != nil {
		select {
		case e.wake <- struct{}{}:
		default:
		}
	}
	return err
}

func (e *candidateEvaluator) configurationChanged() {
	select {
	case e.changed <- struct{}{}:
	default:
	}
}

func (e *candidateEvaluator) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	rescan := true
	rescanFailed := false
	after := ""
	var pendingAfter, pendingThrough int64
	for ctx.Err() == nil {
		more := false
		if rescan {
			records, err := e.store.CandidateRecords(ctx, after, nil, 100)
			if err == nil {
				for _, record := range records {
					if err := e.evaluate(ctx, record.SiteID, record.EventKey, record.SourceScope); err != nil {
						rescanFailed = true
					}
					after = record.Key
				}
				if len(records) < 100 {
					rescan = rescanFailed
					rescanFailed = false
					after = ""
				} else {
					more = true
				}
			}
		}
		batch, err := e.store.PendingDiscoveries(ctx, pendingAfter, pendingThrough, 100)
		if err == nil {
			rows := batch.Items
			pendingThrough = batch.ThroughID
			for _, row := range rows {
				_ = e.evaluate(ctx, row.SiteID, row.EventKey, candidateScope(row))
				pendingAfter = row.ID
			}
			if len(rows) < 100 {
				pendingAfter = 0
				pendingThrough = 0
			} else {
				more = true
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		records, err := e.store.CandidateRecords(ctx, "", &now, 100)
		if err == nil {
			for _, record := range records {
				_ = e.evaluate(ctx, record.SiteID, record.EventKey, record.SourceScope)
			}
		}
		if more {
			select {
			case e.wake <- struct{}{}:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-e.changed:
			rescan = true
			rescanFailed = false
			after = ""
		case <-e.wake:
		case <-ticker.C:
		}
	}
}
