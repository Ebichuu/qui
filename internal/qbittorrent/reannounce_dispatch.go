// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package qbittorrent

import "context"

type ReannounceDispatcher interface {
	DispatchReannounce(context.Context, int, []string) error
}

func (sm *SyncManager) SetReannounceDispatcher(dispatcher ReannounceDispatcher) {
	sm.reannounceDispatcher.Store(dispatcher)
}

// Reannounce is shared by bulk actions and the qB proxy. A dispatch error must
// never cause either caller to fall back to an unguarded request.
func (sm *SyncManager) Reannounce(ctx context.Context, instanceID int, hashes []string) error {
	if dispatcher, ok := sm.reannounceDispatcher.Load().(ReannounceDispatcher); ok {
		return dispatcher.DispatchReannounce(ctx, instanceID, hashes)
	}
	client, err := sm.clientPool.GetClient(ctx, instanceID)
	if err != nil {
		return err
	}
	return client.ReAnnounceTorrentsCtx(ctx, hashes)
}

func (c *Client) ReannounceOnce(ctx context.Context, hashes []string) error {
	return c.singleAttemptClient.ReAnnounceTorrentsCtx(ctx, hashes)
}
