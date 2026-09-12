// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build linux && (amd64 || arm64)

package fileallocation

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReleaseObservation(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "synthetic.bin")
	file, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = file.Write(make([]byte, 1<<20))
	require.NoError(t, err)
	require.NoError(t, file.Sync())
	require.NoError(t, file.Close())
	baseline, err := CaptureRelease(t.Context(), dir, []string{"synthetic.bin"})
	if errors.Is(err, ErrUnknown) {
		t.Skip("test filesystem does not expose exclusive extents and native free space")
	}
	require.NoError(t, err)
	require.Positive(t, baseline.ExpectedBytes)
	_, recovered, err := ObserveRelease(t.Context(), baseline)
	require.NoError(t, err)
	require.False(t, recovered, "an existing file cannot count as released")
	for _, files := range [][]string{{"synthetic.bin", "./synthetic.bin"}, {"../outside"}, {`C:\outside`}, {}} {
		_, err := CaptureRelease(t.Context(), dir, files)
		require.ErrorIs(t, err, ErrUnknown)
	}
	require.NoError(t, os.Remove(name))
	space, recovered, err := ObserveRelease(t.Context(), baseline)
	require.NoError(t, err)
	t.Logf("native deletion: available before=%d after=%d expected=%d recovered=%t (concurrent writes may keep recovery pending)", baseline.Space.Available, space.Available, baseline.ExpectedBytes, recovered)

	// A controlled balance isolates the observation predicate from unrelated
	// writes on the runner's shared filesystem. It is not a deletion receipt.
	controlled := baseline
	controlled.ExpectedBytes = 1
	controlled.Space.Available = 0
	_, recovered, err = ObserveRelease(t.Context(), controlled)
	require.NoError(t, err)
	require.True(t, recovered)
	controlled.Space.Available = math.MaxInt64
	_, recovered, err = ObserveRelease(t.Context(), controlled)
	require.NoError(t, err)
	require.False(t, recovered, "missing files alone are insufficient")
	controlled = baseline
	controlled.Space.ObservedAt = time.Now().Add(time.Hour)
	_, recovered, err = ObserveRelease(t.Context(), controlled)
	require.NoError(t, err)
	require.False(t, recovered)
	controlled.Space.ObservedAt = time.Time{}
	_, _, err = ObserveRelease(t.Context(), controlled)
	require.ErrorIs(t, err, ErrUnknown)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = ObserveRelease(canceled, baseline)
	require.ErrorIs(t, err, context.Canceled)

	// Preserve the old inode so replacement cannot coincidentally reuse it.
	moved := dir + "-moved"
	require.NoError(t, os.Rename(dir, moved))
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	require.NoError(t, os.Mkdir(dir, 0o700))
	_, _, err = ObserveRelease(t.Context(), baseline)
	require.ErrorIs(t, err, ErrUnknown)
}
