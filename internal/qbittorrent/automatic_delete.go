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
)

type automaticDeleteKey struct{}
type AutomaticDeleteGuard interface {
	GuardAutomaticDelete(context.Context, int, []models.DeleteIdentity) error
}

func (sm *SyncManager) SetAutomaticDeleteGuard(guard AutomaticDeleteGuard) {
	sm.automaticDeleteGuard.Store(guard)
}

type automaticDeleteRequest struct {
	owner      string
	candidates []models.DeleteIdentity
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
	return sm.BulkAction(context.WithValue(ctx, automaticDeleteKey{}, automaticDeleteRequest{owner, candidates}), instanceID, hashes, action)
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
		identities = append(identities, models.DeleteIdentity{Hash: torrent.Hash, AddedOn: torrent.AddedOn})
	}
	if len(identities) != len(hashes) {
		return "", errors.New("automatic delete batch changed")
	}
	if guard, ok := sm.automaticDeleteGuard.Load().(AutomaticDeleteGuard); ok {
		if err := guard.GuardAutomaticDelete(ctx, instanceID, identities); err != nil {
			return "", err
		}
		if !sm.HasFreshTorrentCache(ctx, instanceID) {
			return "", errors.New("automatic delete state expired during protection check")
		}
		latest := syncManager.GetTorrentMap(qbt.TorrentFilterOptions{})
		for _, expected := range identities {
			torrent, found := resolveTorrentByVariantHash(latest, expected.Hash)
			if !found || torrent.AddedOn != expected.AddedOn {
				return "", errors.New("automatic delete identity changed during protection check")
			}
		}
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
