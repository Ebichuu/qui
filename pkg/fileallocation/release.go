// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fileallocation

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Space identifies the same open directory and filesystem across observations.
// Available is a measured balance, not bytes attributed to a particular deletion.
type Space struct {
	Device     uint64    `json:"device"`
	RootID     uint64    `json:"rootId"`
	Available  int64     `json:"available"`
	ObservedAt time.Time `json:"observedAt"`
}

type ReleaseBaseline struct {
	Root          string   `json:"root"`
	Files         []string `json:"files"`
	ExpectedBytes int64    `json:"expectedBytes"`
	Space         Space    `json:"space"`
}

func ReadSpace(ctx context.Context, rootPath string) (Space, error) {
	if !Supported || !filepath.IsAbs(rootPath) {
		return Space{}, ErrUnknown
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return Space{}, err
	}
	defer root.Close()
	return readRootSpace(ctx, root)
}

func validReleasePath(name string) bool {
	return filepath.IsLocal(name) && name != "." && !strings.Contains(name, `\`) && (len(name) <= 1 || name[1] != ':')
}

// CaptureRelease records exclusive extents and an existing save-root anchor.
// The caller must separately verify cross-task ownership and task generation.
func CaptureRelease(ctx context.Context, rootPath string, files []string) (ReleaseBaseline, error) {
	baseline := ReleaseBaseline{Root: rootPath, Files: append([]string(nil), files...)}
	if !Supported || !filepath.IsAbs(rootPath) || len(files) == 0 || len(files) > 10000 {
		return baseline, ErrUnknown
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return baseline, err
	}
	defer root.Close()
	before, err := readRootSpace(ctx, root)
	if err != nil {
		return baseline, err
	}
	seen := map[string]bool{}
	for _, name := range files {
		if !validReleasePath(name) || seen[filepath.Clean(name)] {
			return baseline, ErrUnknown
		}
		seen[filepath.Clean(name)] = true
		size, err := exclusiveBytes(ctx, root, name)
		if err != nil {
			return baseline, err
		}
		if size < 0 || size > math.MaxInt64-baseline.ExpectedBytes {
			return baseline, ErrUnknown
		}
		baseline.ExpectedBytes += size
	}
	after, err := readRootSpace(ctx, root)
	if err != nil {
		return baseline, err
	}
	if before.Device != after.Device || before.RootID != after.RootID || baseline.ExpectedBytes <= 0 {
		return baseline, ErrUnknown
	}
	current, err := ReadSpace(ctx, rootPath)
	if err != nil {
		return baseline, err
	}
	if current.Device != after.Device || current.RootID != after.RootID {
		return baseline, ErrUnknown
	}
	baseline.Space = after
	return baseline, nil
}

// ObserveRelease never removes files. A positive result proves missing paths
// and net capacity recovery on the original filesystem, not causal attribution
// of every free byte. Concurrent writes can keep this result pending.
func ObserveRelease(ctx context.Context, baseline ReleaseBaseline) (Space, bool, error) {
	if !Supported || !filepath.IsAbs(baseline.Root) || baseline.Space.ObservedAt.IsZero() {
		return Space{}, false, ErrUnknown
	}
	root, err := os.OpenRoot(baseline.Root)
	if err != nil {
		return Space{}, false, err
	}
	defer root.Close()
	before, err := readRootSpace(ctx, root)
	if err != nil {
		return Space{}, false, err
	}
	if baseline.ExpectedBytes <= 0 || len(baseline.Files) == 0 || len(baseline.Files) > 10000 || baseline.Space.Available < 0 || before.Device != baseline.Space.Device || before.RootID != baseline.Space.RootID {
		return Space{}, false, ErrUnknown
	}
	for _, name := range baseline.Files {
		if err := ctx.Err(); err != nil {
			return Space{}, false, err
		}
		if !validReleasePath(name) {
			return Space{}, false, ErrUnknown
		}
		_, err := root.Lstat(name)
		if err == nil {
			return before, false, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return Space{}, false, err
		}
	}
	after, err := readRootSpace(ctx, root)
	if err != nil {
		return Space{}, false, err
	}
	if before.Device != after.Device || before.RootID != after.RootID {
		return Space{}, false, ErrUnknown
	}
	current, err := ReadSpace(ctx, baseline.Root)
	if err != nil {
		return Space{}, false, err
	}
	if current.Device != after.Device || current.RootID != after.RootID {
		return Space{}, false, ErrUnknown
	}
	return after, after.ObservedAt.After(baseline.Space.ObservedAt) && after.Available >= baseline.Space.Available && after.Available-baseline.Space.Available >= baseline.ExpectedBytes, nil
}
