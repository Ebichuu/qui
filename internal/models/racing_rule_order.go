// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"slices"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
)

// ReorderRules only changes ordering, never fields from a stale browser form.
// A missing or duplicate ID rejects the whole reorder without partial writes.
func (s *RacingStore) ReorderRules(ctx context.Context, ids []int) error {
	unique, err := racingIDs(ids)
	if err != nil || len(unique) != len(ids) {
		return racingInvalid("rule order must contain unique positive IDs")
	}
	_, err = s.configurationWrite(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		query := "SELECT id FROM racing_rules ORDER BY id"
		if dbinterface.DialectOf(s.db) == "postgres" {
			query += " FOR UPDATE"
		}
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return 0, err
		}
		current := []int{}
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			current = append(current, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		slices.Sort(unique)
		if !slices.Equal(current, unique) {
			return 0, racingInvalid("rule list changed; reload before reordering")
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for order, id := range ids {
			if err := racingUpdated(ctx, tx, "UPDATE racing_rules SET sort_order=?,updated_at=? WHERE id=?", order, now, id); err != nil {
				return 0, err
			}
		}
		return 0, nil
	})
	return err
}
