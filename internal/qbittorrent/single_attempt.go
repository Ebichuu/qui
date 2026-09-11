// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package qbittorrent

import (
	"context"
	"net/http"

	qbt "github.com/autobrr/go-qbittorrent"
)

// Reuse transport and the authenticated cookie jar, without another sync cache
// or login session. The library otherwise retries disconnected POST requests.
func newSingleAttemptClient(config qbt.Config, shared *qbt.Client) *qbt.Client {
	config.RetryAttempts = 1
	client := qbt.NewClient(config)
	httpClient := *shared.GetHTTPClient()
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.WithHTTPClient(&httpClient)
	// WithHTTPClient replaces the supplied jar with its own empty jar.
	httpClient.Jar = shared.GetHTTPClient().Jar
	return client
}

// AddTorrentOnce is used after a durable submitted intent. An ambiguous
// transport result must be reconciled by the caller, never replayed here.
func (sm *SyncManager) AddTorrentOnce(ctx context.Context, instanceID int, content []byte, options map[string]string) (*qbt.TorrentAddResponse, error) {
	client, _, err := sm.getClientAndSyncManager(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	response, err := client.singleAttemptClient.AddTorrentFromMemoryCtx(ctx, content, options)
	if err != nil {
		return nil, err
	}
	sm.syncAfterModification(instanceID, client, "add_torrent_from_memory")
	return response, nil
}
