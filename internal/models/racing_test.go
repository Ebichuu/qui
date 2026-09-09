// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package models_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/database"
	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestRacingConfigurationLifecycle(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var db *database.DB
			if engine == "sqlite" {
				db = testdb.NewMigratedSQLite(t, "racing-config")
			} else {
				db = testdb.NewMigratedPostgres(t, "racing-config")
			}
			key := bytes.Repeat([]byte{7}, 32)
			store, err := models.NewRacingStore(db, key)
			require.NoError(t, err)
			ctx := t.Context()
			empty, err := store.Configuration(ctx)
			require.NoError(t, err)
			require.Empty(t, empty.Sites)
			require.Empty(t, empty.Sources)
			require.Empty(t, empty.Groups)
			require.Empty(t, empty.Rules)
			instances, err := models.NewInstanceStore(db, key)
			require.NoError(t, err)
			instance, err := instances.Create(ctx, "Synthetic member", "http://127.0.0.1:1", "test", "test", nil, nil, false, nil)
			require.NoError(t, err)
			secret := "synthetic-cookie-secret"
			siteInput := models.RacingSiteInput{Name: "Synthetic site", BaseURL: "https://example.invalid", Enabled: true, RequestIntervalSeconds: 5, TrackerHosts: []string{"tracker.example.invalid"}, Credential: &secret}
			site, err := store.SaveSite(ctx, 0, siteInput)
			require.NoError(t, err)
			endpoint := "https://example.invalid/private-path?passkey=synthetic-source-secret"
			sourceInput := models.RacingSourceInput{Name: "Synthetic RSS", SiteID: site, Kind: "rss", Enabled: true, IntervalSeconds: 10, URL: &endpoint}
			source, err := store.SaveSource(ctx, 0, sourceInput)
			require.NoError(t, err)
			groupInput := models.RacingGroupInput{Name: "Synthetic group", Enabled: true, InstanceIDs: []int{instance.ID, instance.ID}}
			group, err := store.SaveGroup(ctx, 0, groupInput)
			require.NoError(t, err)
			secondGroup, err := store.SaveGroup(ctx, 0, groupInput)
			require.NoError(t, err)
			ruleInput := models.RacingRuleInput{Name: "Synthetic rule", Enabled: true, SourceIDs: []int{source, source}, AcceptKinds: []string{"official", "free"}, ReceiveWindowSeconds: 900, TargetGroupID: &group, AllowOfficialReclaim: true}
			rule, err := store.SaveRule(ctx, 0, ruleInput)
			require.NoError(t, err)
			config, err := store.Configuration(ctx)
			require.NoError(t, err)
			require.Equal(t, []int{instance.ID}, config.Groups[0].InstanceIDs)
			require.Equal(t, []int{source}, config.Rules[0].SourceIDs)
			require.True(t, config.Sites[0].HasCredential)
			require.True(t, config.Sites[0].Enabled)
			require.True(t, config.Rules[0].Enabled)
			require.True(t, config.Rules[0].AllowOfficialReclaim)
			require.Equal(t, "https://example.invalid", config.Sources[0].URLOrigin)
			payload, err := json.Marshal(config)
			require.NoError(t, err)
			for _, sensitive := range []string{secret, endpoint, "synthetic-source-secret", "private-path", "credential_id", "ciphertext"} {
				require.NotContains(t, string(payload), sensitive)
			}
			// Reconstructing the store models process restart; IDs and references persist.
			restarted, err := models.NewRacingStore(db, key)
			require.NoError(t, err)
			after, err := restarted.Configuration(ctx)
			require.NoError(t, err)
			require.Equal(t, config, after)
			// A rename/disable preserves the server-only secret reference and ciphertext.
			var beforeCipher string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT ciphertext FROM racing_secrets WHERE id=(SELECT credential_id FROM racing_sites WHERE id=?)`, site).Scan(&beforeCipher))
			require.NotContains(t, beforeCipher, secret)
			block, err := aes.NewCipher(key)
			require.NoError(t, err)
			aead, err := cipher.NewGCMWithRandomNonce(block)
			require.NoError(t, err)
			encrypted, err := base64.RawStdEncoding.DecodeString(beforeCipher)
			require.NoError(t, err)
			plaintext, err := aead.Open(nil, nil, encrypted, []byte("site-credential"))
			require.NoError(t, err)
			require.Equal(t, secret, string(plaintext))
			siteInput.Credential = nil
			siteInput.Name = "Renamed site"
			siteInput.Enabled = false
			saved, err := store.SaveSite(ctx, site, siteInput)
			require.NoError(t, err)
			require.Equal(t, site, saved)
			var retained string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT ciphertext FROM racing_secrets WHERE id=(SELECT credential_id FROM racing_sites WHERE id=?)`, site).Scan(&retained))
			require.Equal(t, beforeCipher, retained)
			sourceInput.URL = nil
			sourceInput.Name = "Renamed source"
			saved, err = store.SaveSource(ctx, source, sourceInput)
			require.NoError(t, err)
			require.Equal(t, source, saved)
			groupInput.Name = "Renamed group"
			saved, err = store.SaveGroup(ctx, group, groupInput)
			require.NoError(t, err)
			require.Equal(t, group, saved)
			ruleInput.Name = "Renamed rule"
			ruleInput.Enabled = false
			saved, err = store.SaveRule(ctx, rule, ruleInput)
			require.NoError(t, err)
			require.Equal(t, rule, saved)
			clearCredential := ""
			siteInput.Credential = &clearCredential
			_, err = store.SaveSite(ctx, site, siteInput)
			require.NoError(t, err)
			cleared, err := store.Configuration(ctx)
			require.NoError(t, err)
			require.False(t, cleared.Sites[0].HasCredential)
			require.False(t, cleared.Rules[0].Enabled)
			// Referenced configuration cannot disappear, even while disabled.
			require.ErrorIs(t, store.Delete(ctx, "sites", site), models.ErrRacingReferenced)
			require.ErrorIs(t, store.Delete(ctx, "sources", source), models.ErrRacingReferenced)
			require.ErrorIs(t, store.Delete(ctx, "groups", group), models.ErrRacingReferenced)
			_, err = db.ExecContext(ctx, "DELETE FROM instances WHERE id=?", instance.ID)
			require.Error(t, err)
			// Failed edits are atomic: the previous memberships/rule remain intact.
			groupInput.InstanceIDs = []int{instance.ID, 999999}
			_, err = store.SaveGroup(ctx, group, groupInput)
			require.ErrorIs(t, err, models.ErrRacingInvalid)
			ruleInput.SourceIDs = []int{999999}
			_, err = store.SaveRule(ctx, rule, ruleInput)
			require.ErrorIs(t, err, models.ErrRacingInvalid)
			stable, err := store.Configuration(ctx)
			require.NoError(t, err)
			require.Equal(t, []int{instance.ID}, stable.Groups[0].InstanceIDs)
			require.Equal(t, []int{source}, stable.Rules[0].SourceIDs)
			require.NoError(t, store.Delete(ctx, "rules", rule))
			require.NoError(t, store.Delete(ctx, "groups", group))
			require.NoError(t, store.Delete(ctx, "groups", secondGroup))
			require.NoError(t, store.Delete(ctx, "sources", source))
			require.NoError(t, store.Delete(ctx, "sites", site))
			require.ErrorIs(t, store.Delete(ctx, "sites", site), sql.ErrNoRows)
			var secrets int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM racing_secrets").Scan(&secrets))
			require.Zero(t, secrets)
		})
	}
}

func TestRacingValidationDoesNotEchoSecrets(t *testing.T) {
	db := testdb.NewMigratedSQLite(t, "racing-validation")
	store, err := models.NewRacingStore(db, bytes.Repeat([]byte{1}, 32))
	require.NoError(t, err)
	secretURL := "https://example.invalid/path?passkey=synthetic-hidden"
	_, err = store.SaveSite(context.Background(), 0, models.RacingSiteInput{Name: "test", BaseURL: secretURL, RequestIntervalSeconds: 1})
	require.ErrorIs(t, err, models.ErrRacingInvalid)
	require.NotContains(t, err.Error(), "synthetic-hidden")
	_, err = store.SaveRule(t.Context(), 0, models.RacingRuleInput{Name: "test", SourceIDs: []int{1}, AcceptKinds: []string{"official"}, ReceiveWindowSeconds: 60})
	require.ErrorIs(t, err, models.ErrRacingInvalid)
}
