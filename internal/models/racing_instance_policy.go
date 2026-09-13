// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
)

func (s *RacingStore) SaveInstancePolicy(ctx context.Context, input RacingInstancePolicy) error {
	if input.InstanceID <= 0 || input.MaxConcurrentAdds < 1 || input.MaxConcurrentAdds > 8 || input.MaxActiveDownloads < 1 || input.MinFreeBytes < 0 {
		return racingInvalid("downloader reception limits")
	}
	input.SavePath = strings.TrimSpace(input.SavePath)
	if input.SavePath != "" {
		if _, err := NormalizeRacingPath(input.SavePath); err != nil {
			return err
		}
	}
	if input.AutoTMM && input.SavePath != "" {
		return racingInvalid("automatic management cannot use an explicit save path")
	}
	_, err := s.configurationWrite(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := racingExists(ctx, tx, "instances", input.InstanceID); err != nil {
			return 0, err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO racing_instance_policies(instance_id,enabled,max_concurrent_adds,max_active_downloads,min_free_bytes,save_path,category,auto_tmm,start_paused,reclaim_enabled,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(instance_id) DO UPDATE SET enabled=excluded.enabled,max_concurrent_adds=excluded.max_concurrent_adds,max_active_downloads=excluded.max_active_downloads,min_free_bytes=excluded.min_free_bytes,save_path=excluded.save_path,category=excluded.category,auto_tmm=excluded.auto_tmm,start_paused=excluded.start_paused,reclaim_enabled=excluded.reclaim_enabled,updated_at=excluded.updated_at`, input.InstanceID, boolToInt(input.Enabled), input.MaxConcurrentAdds, input.MaxActiveDownloads, input.MinFreeBytes, input.SavePath, strings.TrimSpace(input.Category), boolToInt(input.AutoTMM), boolToInt(input.StartPaused), boolToInt(input.ReclaimEnabled), time.Now().UTC().Format(time.RFC3339Nano))
		return 0, err
	})
	return err
}

func (s *RacingStore) InstancePolicies(ctx context.Context) ([]RacingInstancePolicy, error) {
	return readInstancePolicies(ctx, s.db)
}

func readInstancePolicies(ctx context.Context, reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]RacingInstancePolicy, error) {
	rows, err := reader.QueryContext(ctx, `SELECT instance_id,enabled,max_concurrent_adds,max_active_downloads,min_free_bytes,save_path,category,auto_tmm,start_paused,reclaim_enabled,updated_at FROM racing_instance_policies ORDER BY instance_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RacingInstancePolicy{}
	for rows.Next() {
		var item RacingInstancePolicy
		if err := rows.Scan(&item.InstanceID, &item.Enabled, &item.MaxConcurrentAdds, &item.MaxActiveDownloads, &item.MinFreeBytes, &item.SavePath, &item.Category, &item.AutoTMM, &item.StartPaused, &item.ReclaimEnabled, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
