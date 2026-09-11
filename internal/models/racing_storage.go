// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models

import (
	"context"
	"strings"
	"time"

	"github.com/autobrr/qui/internal/dbinterface"
	"github.com/autobrr/qui/pkg/pathcmp"
)

// NormalizeRacingPath handles downloader paths, which may belong to another OS.
// It never resolves them against qui's local filesystem.
func NormalizeRacingPath(raw string) (string, error) {
	value := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if strings.ContainsAny(value, "\x00\r\n\t") {
		return "", racingInvalid("storage path contains control characters")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", racingInvalid("storage path must not contain parent traversal")
		}
	}
	windows := pathcmp.IsWindowsDriveAbs(value)
	unc := strings.HasPrefix(value, "//")
	if unc {
		parts := strings.Split(strings.TrimPrefix(value, "//"), "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", racingInvalid("UNC path requires server and share")
		}
	}
	if !windows && !strings.HasPrefix(value, "/") {
		return "", racingInvalid("storage path must be absolute")
	}
	value = pathcmp.NormalizePath(value)
	if unc {
		value = "/" + value
	}
	if windows || unc {
		value = strings.ToLower(value)
	}
	return value, nil
}

func (s *RacingStore) SaveStoragePool(ctx context.Context, id int, input RacingStoragePoolInput) (int, error) {
	name, err := racingName(input.Name)
	if err != nil {
		return 0, err
	}
	return s.configurationWrite(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if id == 0 {
			err := tx.QueryRowContext(ctx, "INSERT INTO racing_storage_pools(name,updated_at) VALUES (?,?) RETURNING id", name, now).Scan(&id)
			return id, err
		}
		return id, racingUpdated(ctx, tx, "UPDATE racing_storage_pools SET name=?,updated_at=? WHERE id=?", name, now, id)
	})
}

func (s *RacingStore) SavePathMapping(ctx context.Context, id int, input RacingPathMappingInput) (int, error) {
	path, err := NormalizeRacingPath(input.Path)
	if err != nil {
		return 0, err
	}
	return s.configurationWrite(ctx, func(tx dbinterface.TxQuerier) (int, error) {
		if err := racingExists(ctx, tx, "instances", input.InstanceID); err != nil {
			return 0, err
		}
		if err := racingExists(ctx, tx, "racing_storage_pools", input.StoragePoolID); err != nil {
			return 0, err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM racing_path_mappings WHERE instance_id=? AND path=? AND id<>?", input.InstanceID, path, id).Scan(&count); err != nil {
			return 0, err
		}
		if count > 0 {
			return 0, racingInvalid("this instance path already has a storage pool")
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if id == 0 {
			err := tx.QueryRowContext(ctx, "INSERT INTO racing_path_mappings(instance_id,storage_pool_id,path,updated_at) VALUES (?,?,?,?) RETURNING id", input.InstanceID, input.StoragePoolID, path, now).Scan(&id)
			return id, err
		}
		return id, racingUpdated(ctx, tx, "UPDATE racing_path_mappings SET instance_id=?,storage_pool_id=?,path=?,updated_at=? WHERE id=?", input.InstanceID, input.StoragePoolID, path, now, id)
	})
}

func readRacingStorage(ctx context.Context, tx dbinterface.TxQuerier, config *RacingConfiguration) error {
	config.StoragePools, config.PathMappings = []RacingStoragePool{}, []RacingPathMapping{}
	rows, err := tx.QueryContext(ctx, "SELECT id,name,updated_at FROM racing_storage_pools ORDER BY id")
	if err != nil {
		return err
	}
	for rows.Next() {
		var pool RacingStoragePool
		if err := rows.Scan(&pool.ID, &pool.Name, &pool.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		config.StoragePools = append(config.StoragePools, pool)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, "SELECT id,instance_id,storage_pool_id,path,updated_at FROM racing_path_mappings ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var mapping RacingPathMapping
		if err := rows.Scan(&mapping.ID, &mapping.InstanceID, &mapping.StoragePoolID, &mapping.Path, &mapping.UpdatedAt); err != nil {
			return err
		}
		config.PathMappings = append(config.PathMappings, mapping)
	}
	return rows.Err()
}
