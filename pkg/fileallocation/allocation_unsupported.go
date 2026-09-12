// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !linux || (!amd64 && !arm64)

package fileallocation

import (
	"context"
	"os"
)

const Supported = false

func ExclusiveBytes(ctx context.Context, _, _ string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return 0, ErrUnknown
}

func exclusiveBytes(ctx context.Context, _ *os.Root, _ string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return 0, ErrUnknown
}
