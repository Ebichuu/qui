// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build linux && (amd64 || arm64)

package fileallocation

import (
	"context"
	"math"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func readRootSpace(ctx context.Context, root *os.Root) (Space, error) {
	if err := ctx.Err(); err != nil {
		return Space{}, err
	}
	dir, err := root.Open(".")
	if err != nil {
		return Space{}, err
	}
	defer dir.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(int(dir.Fd()), &stat); err != nil {
		return Space{}, err
	}
	var volume unix.Statfs_t
	if err := unix.Fstatfs(int(dir.Fd()), &volume); err != nil {
		return Space{}, err
	}
	switch volume.Type {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC:
	default:
		return Space{}, ErrUnknown
	}
	if volume.Bsize <= 0 || volume.Bavail > uint64(math.MaxInt64)/uint64(volume.Bsize) {
		return Space{}, ErrUnknown
	}
	return Space{Device: stat.Dev, RootID: stat.Ino, Available: int64(volume.Bavail) * volume.Bsize, ObservedAt: time.Now().UTC()}, nil
}
