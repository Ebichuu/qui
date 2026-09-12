// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package automations

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"path/filepath"
	"time"

	"github.com/autobrr/qui/pkg/fileallocation"
	"github.com/autobrr/qui/pkg/hardlink"
)

// ReclaimPhysicalBytes supplies evidence only for locally accessible members of
// the target storage pool. Missing file inventories or shared paths remain
// unknown. It sends no downloader actions and never guarantees later release.
func (s *Service) ReclaimPhysicalBytes(ctx context.Context, instanceID int, candidate ReclaimCandidate, poolInstances []int) (*int64, error) {
	if !fileallocation.Supported {
		return nil, nil
	}
	instance, err := s.instanceStore.Get(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if !instance.HasLocalFilesystemAccess || !instance.IsActive || s.backendPool == nil {
		return nil, nil
	}
	backend, err := s.backendPool.GetBackend(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	files, err := s.syncManager.GetTorrentFilesBatch(ctx, instanceID, []string{candidate.Hash})
	if err != nil {
		return nil, err
	}
	inventory, found := files[candidate.Hash]
	if !found || len(inventory) == 0 || len(inventory) > 10000 {
		return nil, nil
	}
	identities := map[hardlink.FileID]bool{}
	var total int64
	for _, file := range inventory {
		full, valid := buildFullPath(candidate.SavePath, file.Name)
		if !valid || file.Size < 0 || file.Progress < 1 {
			return nil, nil
		}
		info, err := backend.Lstat(ctx, full)
		if err != nil || info.FileIDErr != nil || info.FileID.IsZero() || info.Size != file.Size || identities[info.FileID] {
			return nil, err
		}
		relative, err := filepath.Rel(filepath.FromSlash(candidate.SavePath), full)
		if err != nil {
			return nil, err
		}
		bytes, err := fileallocation.ExclusiveBytes(ctx, filepath.FromSlash(candidate.SavePath), relative)
		if err != nil {
			return nil, nil
		}
		if bytes > math.MaxInt64-total {
			return nil, nil
		}
		after, err := backend.Lstat(ctx, full)
		if err != nil || after.FileIDErr != nil || after.FileID != info.FileID || after.Size != info.Size || !after.ModTime.Equal(info.ModTime) || after.Nlinks != info.Nlinks {
			return nil, err
		}
		total += bytes
		identities[info.FileID] = true
	}
	checked := 0
	matchedGeneration := false
	for _, id := range poolInstances {
		member, err := s.instanceStore.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if !member.IsActive || !member.HasLocalFilesystemAccess {
			return nil, nil
		}
		torrents, at, err := s.syncManager.GetObservedTorrents(ctx, id)
		if err != nil {
			return nil, err
		}
		otherBackend, err := s.backendPool.GetBackend(ctx, id)
		if err != nil {
			return nil, err
		}
		hashes := []string{}
		for _, torrent := range torrents {
			if id == instanceID && torrent.Hash == candidate.Hash {
				if torrent.AddedOn != candidate.AddedOn || torrent.SavePath != candidate.SavePath || torrent.Size <= 0 || torrent.Completed < torrent.Size || torrent.AmountLeft != 0 {
					return nil, nil
				}
				matchedGeneration = true
			} else {
				hashes = append(hashes, torrent.Hash)
			}
		}
		allFiles, err := s.syncManager.GetTorrentFilesBatch(ctx, id, hashes)
		if err != nil {
			return nil, err
		}
		for _, torrent := range torrents {
			if id == instanceID && torrent.Hash == candidate.Hash {
				continue
			}
			entries, ok := allFiles[torrent.Hash]
			if !ok {
				return nil, nil
			}
			for _, entry := range entries {
				checked++
				if checked > 10000 || ctx.Err() != nil {
					return nil, ctx.Err()
				}
				full, valid := buildFullPath(torrent.SavePath, entry.Name)
				if !valid {
					return nil, nil
				}
				info, err := otherBackend.Stat(ctx, full)
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				if err != nil || info.FileIDErr != nil {
					return nil, err
				}
				if identities[info.FileID] {
					return nil, nil
				}
			}
		}
		if time.Since(at) > 5*time.Second {
			return nil, nil
		}
	}
	if !matchedGeneration || time.Since(candidate.ObservedAt) > 5*time.Second {
		return nil, nil
	}
	return &total, nil
}
