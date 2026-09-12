// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package fileallocation reads conservative file allocation evidence. It never
// equates logical length with disk release or operates on raw block devices.
package fileallocation

import "errors"

var ErrUnknown = errors.New("exclusive file allocation is unknown")
