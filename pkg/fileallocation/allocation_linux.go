// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build linux && (amd64 || arm64)

package fileallocation

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Layout and flag meanings follow Linux UAPI linux/fiemap.h:
// https://docs.kernel.org/filesystems/fiemap.html
// Linux's standard _IOWR('f', 11, struct fiemap) request uses a 32-byte header.
const Supported = true

const fiemapRequest = 0xc020660b
const extentLast = 0x1
const extentUnwritten = 0x800

type extent struct {
	Logical, Physical, Length uint64
	Reserved64                [2]uint64
	Flags                     uint32
	Reserved                  [3]uint32
}
type extentRequest struct {
	Start, Length                  uint64
	Flags, Mapped, Count, Reserved uint32
	Extents                        [128]extent
}

// ExclusiveBytes reads allocated, unshared extents for one regular file under
// a trusted local save root. Hardlinks, shared/encoded/pending extents, changes
// during enumeration and unsupported filesystems retain unknown evidence.
func ExclusiveBytes(ctx context.Context, rootPath, relative string) (int64, error) {
	if !filepath.IsAbs(rootPath) || !filepath.IsLocal(relative) {
		return 0, ErrUnknown
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	file, err := root.OpenFile(relative, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	anchor, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	var rootStat unix.Stat_t
	err = unix.Fstat(int(anchor.Fd()), &rootStat)
	anchor.Close()
	if err != nil {
		return 0, err
	}
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &filesystem); err != nil {
		return 0, err
	}
	// Overlay removal may only create a whiteout, with no backing-space release.
	// Restrict evidence to filesystems with native extent ownership reporting.
	switch filesystem.Type {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC:
	default:
		return 0, ErrUnknown
	}
	var before, after unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &before); err != nil {
		return 0, err
	}
	if before.Dev != rootStat.Dev || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 {
		return 0, ErrUnknown
	}
	var total int64
	var start uint64
	complete := false
	for batch := 0; batch < 32; batch++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		request := extentRequest{Start: start, Length: math.MaxUint64 - start, Count: 128}
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, file.Fd(), fiemapRequest, uintptr(unsafe.Pointer(&request)))
		runtime.KeepAlive(file)
		if errno != 0 {
			return 0, ErrUnknown
		}
		if request.Mapped > request.Count {
			return 0, ErrUnknown
		}
		if request.Mapped == 0 {
			complete = true
			break
		}
		for _, item := range request.Extents[:request.Mapped] {
			// No unknown flag may accidentally become evidence of exclusive capacity.
			if item.Flags & ^uint32(extentLast|extentUnwritten) != 0 || item.Length == 0 || item.Logical < start || item.Logical > math.MaxUint64-item.Length || item.Length > uint64(math.MaxInt64-total) {
				return 0, ErrUnknown
			}
			total += int64(item.Length)
			start = item.Logical + item.Length
			if item.Flags&extentLast != 0 {
				complete = true
			}
		}
		if complete {
			break
		}
	}
	if !complete {
		return 0, ErrUnknown
	}
	if err := unix.Fstat(int(file.Fd()), &after); err != nil {
		return 0, err
	}
	if before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size || before.Blocks != after.Blocks || before.Nlink != after.Nlink || before.Ctim != after.Ctim || before.Mtim != after.Mtim {
		return 0, ErrUnknown
	}
	return total, nil
}
