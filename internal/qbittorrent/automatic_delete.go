// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package qbittorrent

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/pkg/fileallocation"
)

type automaticDeleteKey struct{}
type AutomaticDeleteGuard interface {
	GuardAutomaticDelete(context.Context, int, []models.DeleteIdentity) error
}

func (sm *SyncManager) SetAutomaticDeleteGuard(guard AutomaticDeleteGuard) {
	sm.automaticDeleteGuard.Store(guard)
}

type automaticDeleteRequest struct {
	owner       string
	candidates  []models.DeleteIdentity
	plan        *models.RacingReclaimPlan
	baseline    fileallocation.ReleaseBaseline
	commitments string
	savePath    string
}

func (sm *SyncManager) SetAutomaticDeleteStore(store *models.RacingStore) {
	sm.automaticDeleteStore.Store(store)
}

// AutomaticDelete shares durable ownership across automatic callers. Manual API
// requests remain explicit external changes, not automatically retried intents.
func (sm *SyncManager) AutomaticDelete(ctx context.Context, instanceID int, candidates []models.DeleteIdentity, action, owner string) error {
	if action != models.DeleteModeKeepFiles && action != models.DeleteModeWithFiles {
		return errors.New("unsupported automatic delete action")
	}
	if err := sm.ReconcileAutomaticDeletes(ctx, instanceID); err != nil {
		return err
	}
	hashes := make([]string, 0, len(candidates))
	for _, item := range candidates {
		hashes = append(hashes, item.Hash)
	}
	return sm.BulkAction(context.WithValue(ctx, automaticDeleteKey{}, automaticDeleteRequest{owner: owner, candidates: candidates}), instanceID, hashes, action)
}

// ReclaimDelete uses the common single-attempt delete path, but the guarded
// claim atomically consumes the official plan budget instead of claiming twice.
func (sm *SyncManager) ReclaimDelete(ctx context.Context, plan models.RacingReclaimPlan, baseline fileallocation.ReleaseBaseline, commitments, savePath string) error {
	if len(plan.Items) == 0 {
		return models.ErrRacingStale
	}
	item := plan.Items[0]
	request := automaticDeleteRequest{owner: "official-reclaim", candidates: []models.DeleteIdentity{{Hash: item.Hash, AddedOn: item.AddedOn}}, plan: &plan, baseline: baseline, commitments: commitments, savePath: savePath}
	return sm.BulkAction(context.WithValue(ctx, automaticDeleteKey{}, request), plan.InstanceID, []string{item.Hash}, models.DeleteModeWithFiles)
}

func (sm *SyncManager) beginAutomaticDelete(ctx context.Context, instanceID int, syncManager *qbt.SyncManager, hashes []string, action string) (string, error) {
	request, ok := ctx.Value(automaticDeleteKey{}).(automaticDeleteRequest)
	if !ok {
		return "", nil
	}
	store := sm.automaticDeleteStore.Load()
	if store == nil {
		return "", errors.New("automatic delete store unavailable")
	}
	if !sm.HasFreshTorrentCache(ctx, instanceID) {
		return "", errors.New("automatic delete requires fresh torrent state")
	}
	current := syncManager.GetTorrentMap(qbt.TorrentFilterOptions{})
	identities := make([]models.DeleteIdentity, 0, len(request.candidates))
	for _, expected := range request.candidates {
		torrent, found := resolveTorrentByVariantHash(current, expected.Hash)
		if !found || expected.AddedOn <= 0 || torrent.AddedOn != expected.AddedOn {
			return "", errors.New("automatic delete identity changed or is unknown")
		}
		if request.plan != nil && (torrent.SavePath != request.savePath || torrent.Size <= 0 || torrent.Completed < torrent.Size || torrent.AmountLeft != 0) {
			return "", errors.New("official reclaim data changed")
		}
		identities = append(identities, models.DeleteIdentity{Hash: torrent.Hash, AddedOn: torrent.AddedOn})
	}
	if len(identities) != len(hashes) {
		return "", errors.New("automatic delete batch changed")
	}
	if guard, ok := sm.automaticDeleteGuard.Load().(AutomaticDeleteGuard); ok {
		if request.plan != nil {
			strict, ok := guard.(interface {
				GuardReclaimDelete(context.Context, int, []models.DeleteIdentity) error
			})
			if !ok {
				return "", errors.New("official reclaim protection unavailable")
			}
			if err := strict.GuardReclaimDelete(ctx, instanceID, identities); err != nil {
				return "", err
			}
		} else if err := guard.GuardAutomaticDelete(ctx, instanceID, identities); err != nil {
			return "", err
		}
		if !sm.HasFreshTorrentCache(ctx, instanceID) {
			return "", errors.New("automatic delete state expired during protection check")
		}
		latest := syncManager.GetTorrentMap(qbt.TorrentFilterOptions{})
		for _, expected := range identities {
			torrent, found := resolveTorrentByVariantHash(latest, expected.Hash)
			if request.plan != nil && (!found || torrent.SavePath != request.savePath || torrent.Size <= 0 || torrent.Completed < torrent.Size || torrent.AmountLeft != 0) {
				return "", errors.New("official reclaim data changed during protection check")
			}
			if !found || torrent.AddedOn != expected.AddedOn {
				return "", errors.New("automatic delete identity changed during protection check")
			}
		}
	}
	if request.plan != nil {
		if _, ok := sm.automaticDeleteGuard.Load().(AutomaticDeleteGuard); !ok {
			return "", errors.New("official reclaim protection unavailable")
		}
		operation := rand.Text()
		if len(identities) != 1 || action != models.DeleteModeWithFiles {
			return "", models.ErrRacingInvalid
		}
		if err := store.BeginReclaimDelete(ctx, request.plan.CandidateKey, operation, request.baseline, identities[0], instanceID, request.commitments); err != nil {
			return "", err
		}
		return operation, nil
	}
	operation := rand.Text()
	if err := store.BeginAutomaticDelete(ctx, instanceID, operation, request.owner, action, identities); err != nil {
		return "", err
	}
	return operation, nil
}

// A fresh snapshot after an accepted request confirms task absence. Unknown
// requests retain ownership until explicit reconciliation; a still-running
// remote request could otherwise delete a re-added task. A present task with a
// different generation is never treated as evidence that an old request finished.
func (sm *SyncManager) ReconcileAutomaticDeletes(ctx context.Context, instanceID int) error {
	store := sm.automaticDeleteStore.Load()
	if store == nil {
		return errors.New("automatic delete store unavailable")
	}
	pending, err := store.PendingAutomaticDeletes(ctx, instanceID)
	if err != nil || len(pending) == 0 {
		return err
	}
	client, err := sm.clientPool.GetClientOffline(ctx, instanceID)
	if err != nil {
		return err
	}
	syncManager := client.GetSyncManager()
	if syncManager == nil || !sm.HasFreshTorrentCache(ctx, instanceID) {
		return nil
	}
	at := syncManager.LastSuccessfulSyncTime()
	torrents := syncManager.GetTorrentMap(qbt.TorrentFilterOptions{})
	for _, item := range pending {
		if item.State != "accepted" {
			continue
		}
		if at.Before(item.SubmittedAt.Add(time.Second)) {
			continue
		}
		if _, found := resolveTorrentByVariantHash(torrents, item.Hash); !found {
			if err := store.ConfirmAutomaticDelete(ctx, item, at); err != nil {
				return err
			}
		}
	}
	return nil
}
