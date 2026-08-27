// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestDailyTransferStatsStoreRecordsDeltasAndRollover(t *testing.T) {
	db := testdb.NewMigratedSQLite(t, "daily-transfer")
	instanceID := insertDailyTransferTestInstance(t, db)
	store := models.NewDailyTransferStatsStore(db)
	ctx := context.Background()
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	start := time.Date(2026, time.August, 27, 23, 59, 0, 0, location)

	stats, err := store.Record(ctx, instanceID, start, 1_000, 2_000)
	require.NoError(t, err)
	require.Equal(t, int64(0), stats.Downloaded)
	require.Equal(t, int64(0), stats.Uploaded)

	stats, err = store.Record(ctx, instanceID, start.Add(30*time.Second), 1_400, 2_900)
	require.NoError(t, err)
	require.Equal(t, int64(400), stats.Downloaded)
	require.Equal(t, int64(900), stats.Uploaded)

	stats, err = store.Record(ctx, instanceID, start.Add(2*time.Minute), 1_550, 3_200)
	require.NoError(t, err)
	require.Equal(t, "2026-08-28", stats.Date)
	require.Equal(t, int64(150), stats.Downloaded)
	require.Equal(t, int64(300), stats.Uploaded)
}

func TestDailyTransferStatsStoreHandlesCounterReset(t *testing.T) {
	db := testdb.NewMigratedSQLite(t, "daily-transfer-reset")
	instanceID := insertDailyTransferTestInstance(t, db)
	store := models.NewDailyTransferStatsStore(db)
	ctx := context.Background()
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

	_, err := store.Record(ctx, instanceID, now, 10_000, 20_000)
	require.NoError(t, err)
	stats, err := store.Record(ctx, instanceID, now.Add(time.Minute), 250, 500)
	require.NoError(t, err)
	require.Equal(t, int64(250), stats.Downloaded)
	require.Equal(t, int64(500), stats.Uploaded)
}

func insertDailyTransferTestInstance(t *testing.T, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) int {
	t.Helper()
	ctx := context.Background()
	for _, value := range []string{"daily-transfer", "http://127.0.0.1:8080", "user"} {
		_, err := db.ExecContext(ctx, "INSERT OR IGNORE INTO string_pool (value) VALUES (?)", value)
		require.NoError(t, err)
	}
	var nameID, hostID, usernameID int64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT id FROM string_pool WHERE value = ?", "daily-transfer").Scan(&nameID))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT id FROM string_pool WHERE value = ?", "http://127.0.0.1:8080").Scan(&hostID))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT id FROM string_pool WHERE value = ?", "user").Scan(&usernameID))

	result, err := db.ExecContext(context.Background(), `
		INSERT INTO instances (name_id, host_id, username_id, password_encrypted, is_active)
		VALUES (?, ?, ?, 'password', 1)
	`, nameID, hostID, usernameID)
	require.NoError(t, err)
	id, err := result.LastInsertId()
	require.NoError(t, err)
	return int(id)
}
