// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package qbittorrent

import qbt "github.com/autobrr/go-qbittorrent"

// SyncEventFanout shares notifications without replacing the original SSE sink.
// Like SyncEventSink, each recipient must return quickly and treat data as read-only.
type SyncEventFanout []SyncEventSink

func (s SyncEventFanout) HandleMainData(id int, data *qbt.MainData) {
	for _, sink := range s {
		sink.HandleMainData(id, data)
	}
}
func (s SyncEventFanout) HandleTrackerHealthUpdated(id int) {
	for _, sink := range s {
		sink.HandleTrackerHealthUpdated(id)
	}
}
func (s SyncEventFanout) HandleSyncError(id int, err error) {
	for _, sink := range s {
		sink.HandleSyncError(id, err)
	}
}
