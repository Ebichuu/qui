// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package reannounce

import (
	"encoding/json"
	"testing"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/require"
)

func TestTrackerObservationIdentityAndPrivacy(t *testing.T) {
	rows := trackerObservations([]qbt.TorrentTracker{
		{Url: "https://a.example.invalid/announce?passkey=synthetic-secret", Status: qbt.TrackerStatusOK},
		{Url: "https://a.example.invalid/announce?passkey=other-secret", Status: qbt.TrackerStatusNotWorking, Message: "unknown error with synthetic-secret"},
		{Url: "https://b.example.invalid/announce", Status: qbt.TrackerStatusOK, Message: "Torrent not registered"},
		{Url: "** [DHT] **", Status: qbt.TrackerStatusDisabled},
	})
	require.Len(t, rows, 3)
	require.Equal(t, "reported_working", rows[0].State)
	require.Equal(t, "error_unknown", rows[1].State)
	require.Equal(t, "unregistered", rows[2].State)
	require.NotEqual(t, rows[0].Key, rows[1].Key)
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "synthetic-secret")
	require.NotContains(t, string(raw), "/announce")
	require.Len(t, rows[1].MessageDigest, 64)
}
