// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"fmt"
	"testing"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func TestAnalysisEndpointsAndMissingEvidence(t *testing.T) {
	now := time.Now()
	history := models.RacingAnalysisHistory{StartedAt: now, Peers: map[string]models.RacingPeerHistory{}}
	peers := map[string]qbt.TorrentPeer{"[2001:db8::1]:6881": {Downloaded: 12, Uploaded: 3}}
	require.Equal(t, "observed", mergeAnalysisPeers(&history, peers, now))
	peer := history.Peers["[2001:db8::1]:6881"]
	require.Equal(t, "2001:db8::1", peer.IP)
	require.Equal(t, 6881, peer.Port)
	require.Equal(t, 1, peer.Samples)
	require.Nil(t, peer.MaxProgress, "omitted progress cannot imply completion")
	require.Equal(t, "partial", mergeAnalysisPeers(&history, map[string]qbt.TorrentPeer{"invalid": {}}, now.Add(time.Second)))
	require.Nil(t, history.Peers["[2001:db8::1]:6881"].AbsentAt, "failed sample cannot mark removal")
	require.Equal(t, "observed", mergeAnalysisPeers(&history, nil, now.Add(2*time.Second)))
	require.NotNil(t, history.Peers["[2001:db8::1]:6881"].AbsentAt)
	peers["[2001:db8::1]:6881"] = qbt.TorrentPeer{Downloaded: 1, Uploaded: 1}
	require.Equal(t, "observed", mergeAnalysisPeers(&history, peers, now.Add(3*time.Second)))
	peer = history.Peers["[2001:db8::1]:6881"]
	require.Nil(t, peer.AbsentAt)
	require.Equal(t, 2, peer.Samples)
	require.Equal(t, 1, peer.CounterResets)
	require.Equal(t, int64(1), peer.Downloaded, "reset is retained, not merged into a false total")
}

func TestAnalysisBoundedPeersAndHashVariants(t *testing.T) {
	history := models.RacingAnalysisHistory{Peers: map[string]models.RacingPeerHistory{}}
	peers := map[string]qbt.TorrentPeer{}
	for i := 1; i <= 1001; i++ {
		peers[fmt.Sprintf("192.0.2.1:%d", i)] = qbt.TorrentPeer{}
	}
	require.Equal(t, "partial", mergeAnalysisPeers(&history, peers, time.Now()))
	require.Len(t, history.Peers, 1000)
	require.True(t, history.Truncated)
	require.Nil(t, history.LastSuccess)
	torrents := []qbt.Torrent{{Hash: "real-index", InfohashV2: "ABCD", AddedOn: 123}, {Hash: "other", Name: "same-name"}}
	torrent, ok := analysisTorrent(torrents, models.RacingAddPlan{HashV2: "abcd"})
	require.True(t, ok)
	require.Equal(t, "real-index", torrent.Hash)
	_, ok = analysisTorrent(torrents, models.RacingAddPlan{HashV1: "missing"})
	require.False(t, ok)
}

func TestAnalysisCoverageIncludesRestartAndPartial(t *testing.T) {
	now := time.Now()
	history := models.RacingAnalysisHistory{StartedAt: now, Samples: []models.RacingAnalysisSample{{At: now.Add(time.Second), State: "observed"}, {At: now.Add(6 * time.Second), State: "partial"}}}
	history.Coverage(now.Add(time.Minute))
	require.Equal(t, 12, history.ExpectedSamples)
	require.Equal(t, 1, history.SuccessfulSamples)
	require.Equal(t, 11, history.MissingSamples)
	history.Coverage(now.Add(time.Hour))
	require.Equal(t, 240, history.ExpectedSamples)
	require.Equal(t, 239, history.MissingSamples)
	require.Equal(t, "unavailable", history.ASNState)
	require.Equal(t, "unsupported", history.RankingState)
}
