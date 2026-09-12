// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fileallocation

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReleasePathBoundaries(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../file", "/file", `\file`, `C:\file`, `C:file`, `\\server\share`, `dir\file`} {
		require.False(t, validReleasePath(name), name)
	}
	for _, name := range []string{"file", "directory/file"} {
		require.True(t, validReleasePath(name), name)
	}
}

func TestUnsupportedReleaseEvidence(t *testing.T) {
	if Supported {
		t.Skip("unsupported platform check")
	}
	_, err := ReadSpace(t.Context(), t.TempDir())
	require.ErrorIs(t, err, ErrUnknown)
	_, err = CaptureRelease(t.Context(), t.TempDir(), []string{"file"})
	require.ErrorIs(t, err, ErrUnknown)
	_, recovered, err := ObserveRelease(t.Context(), ReleaseBaseline{Root: t.TempDir()})
	require.ErrorIs(t, err, ErrUnknown)
	require.False(t, recovered)
}
