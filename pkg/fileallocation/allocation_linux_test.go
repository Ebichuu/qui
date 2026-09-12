// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build linux && (amd64 || arm64)

package fileallocation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func TestFiemapLayout(t *testing.T) {
	var request extentRequest
	require.EqualValues(t, 32, unsafe.Offsetof(request.Extents))
	require.EqualValues(t, 56, unsafe.Sizeof(extent{}))
}

func TestExclusiveFileEvidence(t *testing.T) {
	dir := t.TempDir()
	file, err := os.OpenFile(filepath.Join(dir, "synthetic.bin"), os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = file.Write(make([]byte, 1<<20))
	require.NoError(t, err)
	require.NoError(t, file.Sync())
	require.NoError(t, file.Close())
	size, err := ExclusiveBytes(t.Context(), dir, "synthetic.bin")
	if errors.Is(err, ErrUnknown) {
		t.Skip("test filesystem does not expose unshared FIEMAP extents")
	}
	require.NoError(t, err)
	require.Positive(t, size)
	require.NoError(t, os.Link(filepath.Join(dir, "synthetic.bin"), filepath.Join(dir, "linked.bin")))
	_, err = ExclusiveBytes(t.Context(), dir, "synthetic.bin")
	require.ErrorIs(t, err, ErrUnknown)
	_, err = ExclusiveBytes(t.Context(), dir, "../outside")
	require.ErrorIs(t, err, ErrUnknown)
}
