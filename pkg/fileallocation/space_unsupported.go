// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !linux || (!amd64 && !arm64)

package fileallocation

import (
	"context"
	"os"
)

func readRootSpace(ctx context.Context, _ *os.Root) (Space, error) {
	if err := ctx.Err(); err != nil {
		return Space{}, err
	}
	return Space{}, ErrUnknown
}
