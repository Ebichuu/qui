// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/database"
	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestRacingRuleOrderPreservesFieldsAndRejectsPartialLists(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var db *database.DB
			if engine == "sqlite" {
				db = testdb.NewMigratedSQLite(t, "racing-order")
			} else {
				db = testdb.NewMigratedPostgres(t, "racing-order")
			}
			store, err := models.NewRacingStore(db, bytes.Repeat([]byte{2}, 32))
			require.NoError(t, err)
			site, err := store.SaveSite(t.Context(), 0, models.RacingSiteInput{Name: "Example", BaseURL: "https://example.invalid", RequestIntervalSeconds: 1})
			require.NoError(t, err)
			address := "https://example.invalid/rss"
			source, err := store.SaveSource(t.Context(), 0, models.RacingSourceInput{Name: "Example feed", SiteID: site, Kind: "rss", IntervalSeconds: 60, URL: &address})
			require.NoError(t, err)
			group, err := store.SaveGroup(t.Context(), 0, models.RacingGroupInput{Name: "Example group"})
			require.NoError(t, err)
			input := models.RacingRuleInput{Name: "First", Enabled: true, SourceIDs: []int{source}, AcceptKinds: []string{"official"}, ReceiveWindowSeconds: 900, TargetGroupID: &group, AllowOfficialReclaim: true}
			first, err := store.SaveRule(t.Context(), 0, input)
			require.NoError(t, err)
			input.Name = "Second"
			input.AllowOfficialReclaim = false
			second, err := store.SaveRule(t.Context(), 0, input)
			require.NoError(t, err)
			require.NoError(t, store.ReorderRules(t.Context(), []int{second, first}))
			configuration, err := store.Configuration(t.Context())
			require.NoError(t, err)
			require.Equal(t, second, configuration.Rules[0].ID)
			require.Equal(t, first, configuration.Rules[1].ID)
			require.Equal(t, "First", configuration.Rules[1].Name)
			require.True(t, configuration.Rules[1].AllowOfficialReclaim)
			require.False(t, configuration.Rules[0].AllowOfficialReclaim)
			for _, ids := range [][]int{{first}, {second, second}, {second, first, 999}, {first, 0}} {
				require.ErrorIs(t, store.ReorderRules(t.Context(), ids), models.ErrRacingInvalid)
				current, err := store.Configuration(t.Context())
				require.NoError(t, err)
				require.Equal(t, configuration.Rules, current.Rules)
			}
		})
	}
}
