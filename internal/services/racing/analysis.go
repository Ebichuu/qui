// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"github.com/rs/zerolog/log"

	"github.com/autobrr/qui/internal/models"
)

type analysisClient interface {
	GetObservedTorrents(context.Context, int) ([]qbt.Torrent, time.Time, error)
	GetTorrentPeers(context.Context, int, string) (*qbt.TorrentPeersResponse, error)
}

// One independent worker has no queue or execution slots. It processes at most
// one task per second and uses a two-second request budget; missed opportunities
// remain in the time-based coverage denominator, including across restarts.
func (s *Service) runAnalysis(ctx context.Context, store *models.RacingStore) {
	s.mu.RLock()
	path := s.asnDatabasePath
	s.mu.RUnlock()
	var asn *asnDatabase
	if path != "" {
		var err error
		asn, err = openASNDatabase(path)
		if err != nil {
			log.Warn().Err(err).Msg("Offline ASN database unavailable; Peer sampling continues")
		} else {
			defer asn.reader.Close()
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	nextPrune := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			taskCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			if !now.Before(nextPrune) {
				_ = store.PruneAnalysisHistory(taskCtx, now)
				nextPrune = now.Add(time.Minute)
			}
			s.mu.RLock()
			client, ok := s.executionClient.(analysisClient)
			s.mu.RUnlock()
			if ok {
				intent, err := store.AnalysisNext(taskCtx, now)
				if err == nil && intent != nil {
					s.sampleAnalysis(taskCtx, store, client, *intent, now, asn)
				}
			}
			cancel()
		}
	}
}

func (s *Service) sampleAnalysis(ctx context.Context, store *models.RacingStore, client analysisClient, intent models.RacingAddIntent, now time.Time, asn *asnDatabase) {
	history, err := store.AnalysisHistory(ctx, intent.CandidateKey)
	if err != nil {
		return
	}
	if history == nil {
		return
	}
	if len(history.Samples) >= 240 {
		return
	}
	state := "snapshot_unavailable"
	torrents, _, err := client.GetObservedTorrents(ctx, intent.InstanceID)
	if err == nil {
		torrent, found := analysisTorrent(torrents, intent.Plan)
		state = "task_absent"
		if found {
			state = "generation_unknown"
			if torrent.AddedOn > 0 {
				state = "generation_changed"
				if torrent.AddedOn <= history.StartedAt.Unix() && (history.AddedOn == 0 || history.AddedOn == torrent.AddedOn) {
					history.Hash, history.AddedOn = torrent.Hash, torrent.AddedOn
					history.LastVisible = &now
					peers, err := client.GetTorrentPeers(ctx, intent.InstanceID, torrent.Hash)
					state = "peers_unavailable"
					if err == nil && peers != nil {
						// Do not merge a response collected across removal/re-addition of a hash.
						after, _, err := client.GetObservedTorrents(ctx, intent.InstanceID)
						current, found := analysisTorrent(after, intent.Plan)
						state = "snapshot_unavailable"
						if err == nil && found && current.AddedOn == history.AddedOn {
							state = mergeAnalysisPeers(history, peers.Peers, now)
						}
					}
				}
			}
		}
	}
	history.Samples = append(history.Samples, models.RacingAnalysisSample{At: now, State: state})
	asn.enrich(ctx, history, now)
	history.Coverage(now)
	// A request timeout must still leave a failed sample, without extending the
	// network request or holding the primary runner. Application cancellation wins.
	if ctx.Err() == nil {
		_ = store.SaveAnalysisHistory(ctx, *history)
		return
	}
	if ctx.Err() == context.DeadlineExceeded {
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = store.SaveAnalysisHistory(saveCtx, *history)
	}
}

func analysisTorrent(torrents []qbt.Torrent, plan models.RacingAddPlan) (qbt.Torrent, bool) {
	for _, torrent := range torrents {
		for _, hash := range []string{torrent.Hash, torrent.InfohashV1, torrent.InfohashV2} {
			if hash != "" && (strings.EqualFold(hash, plan.HashV1) || strings.EqualFold(hash, plan.HashV2)) {
				return torrent, true
			}
		}
	}
	return qbt.Torrent{}, false
}

func peerEndpoint(key string, peer qbt.TorrentPeer) (string, int, bool) {
	ip, err := netip.ParseAddr(strings.Trim(peer.IP, "[]"))
	port := peer.Port
	if err != nil || port < 1 || port > 65535 {
		host, rawPort, splitErr := net.SplitHostPort(key)
		if splitErr != nil {
			return "", 0, false
		}
		ip, err = netip.ParseAddr(host)
		port, _ = strconv.Atoi(rawPort)
	}
	if err != nil || ip.Zone() != "" || port < 1 || port > 65535 {
		return "", 0, false
	}
	return ip.Unmap().String(), port, true
}

func mergeAnalysisPeers(history *models.RacingAnalysisHistory, peers map[string]qbt.TorrentPeer, now time.Time) string {
	keys := make([]string, 0, len(peers))
	for key := range peers {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	seen := map[string]bool{}
	complete := len(peers) <= models.RacingAnalysisPeerLimit
	for _, key := range keys[:min(len(keys), models.RacingAnalysisPeerLimit)] {
		peer := peers[key]
		ip, port, ok := peerEndpoint(key, peer)
		if !ok {
			complete = false
			continue
		}
		endpoint := net.JoinHostPort(ip, strconv.Itoa(port))
		if seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		previous, exists := history.Peers[endpoint]
		if !exists {
			if len(history.Peers) >= models.RacingAnalysisPeerLimit {
				complete = false
				continue
			}
			previous = models.RacingPeerHistory{IP: ip, Port: port, FirstSeen: now}
		}
		if exists && (peer.Downloaded < previous.Downloaded || peer.Uploaded < previous.Uploaded) {
			previous.CounterResets++
		}
		previous.Downloaded = max(peer.Downloaded, 0)
		previous.Uploaded = max(peer.Uploaded, 0)
		previous.LastSeen = now
		previous.AbsentAt = nil
		previous.Samples++
		if peer.HasProgress() && peer.Progress >= 0 && peer.Progress <= 1 {
			if previous.MaxProgress == nil || peer.Progress > *previous.MaxProgress {
				value := peer.Progress
				previous.MaxProgress = &value
			}
			if peer.Progress == 1 && previous.FirstComplete == nil {
				value := now
				previous.FirstComplete = &value
			}
		}
		history.Peers[endpoint] = previous
	}
	// Only a complete successful snapshot proves an endpoint is no longer visible.
	if complete {
		for key, peer := range history.Peers {
			if !seen[key] && peer.AbsentAt == nil {
				value := now
				peer.AbsentAt = &value
				history.Peers[key] = peer
			}
		}
		history.LastSuccess = &now
		return "observed"
	}
	history.Truncated = true
	return "partial"
}
