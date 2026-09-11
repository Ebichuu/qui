// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"errors"

	"github.com/autobrr/qui/internal/dbinterface"
)

var ErrRacingStale = errors.New("racing decision changed; reevaluate before submission")
var ErrRacingCapacity = errors.New("racing capacity is already committed")
var ErrRacingIntentExists = errors.New("racing candidate or torrent already has an intent")
var ErrRacingIntentState = errors.New("racing intent cannot make this transition")

// Configuration mutations acquire the same short transaction lock as intent
// mutations. Discovery writes do not increment this revision. No network call
// belongs in these transactions.
func (s *RacingStore) configurationWrite(ctx context.Context, fn func(dbinterface.TxQuerier) (int, error)) (int, error) {
	return s.write(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if _, err := tx.ExecContext(ctx, "UPDATE racing_configuration_revision SET revision=revision+1 WHERE id=1"); err != nil {
			return 0, err
		}
		return fn(tx)
	})
}

func (s *RacingStore) lockExecution(ctx context.Context, tx dbinterface.TxQuerier, expected *int64) error {
	// UPDATE also acquires a write lock on SQLite before reading the ledger.
	var revision int64
	err := tx.QueryRowContext(ctx, "UPDATE racing_configuration_revision SET revision=revision WHERE id=1 RETURNING revision").Scan(&revision)
	if err != nil {
		return err
	}
	if expected != nil && revision != *expected {
		return ErrRacingStale
	}
	return nil
}
