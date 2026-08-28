// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestDashboardSettingsStorePersistsServerStatsSort(t *testing.T) {
	store := models.NewDashboardSettingsStore(testdb.NewMigratedSQLite(t, "dashboard-settings-sort"))
	ctx := context.Background()

	settings, err := store.GetByUserID(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "instance", settings.ServerStatsSortColumn)
	require.Equal(t, "asc", settings.ServerStatsSortDir)

	settings, err = store.Update(ctx, 1, &models.DashboardSettingsInput{
		ServerStatsSortColumn: "uploadedToday",
		ServerStatsSortDir:    "desc",
	})
	require.NoError(t, err)
	require.Equal(t, "uploadedToday", settings.ServerStatsSortColumn)
	require.Equal(t, "desc", settings.ServerStatsSortDir)
}
