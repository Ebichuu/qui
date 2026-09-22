// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
)

func writeASNFixture(t *testing.T, kind string) string {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: kind, Description: map[string]string{"en": "Synthetic ASN fixture"}, BuildEpoch: 1700000000, IncludeReservedNetworks: true})
	require.NoError(t, err)
	for _, cidr := range []string{"192.0.2.0/24", "2001:db8::/32"} {
		_, network, err := net.ParseCIDR(cidr)
		require.NoError(t, err)
		require.NoError(t, tree.Insert(network, mmdbtype.Map{"autonomous_system_number": mmdbtype.Uint32(64512), "autonomous_system_organization": mmdbtype.String("Synthetic ASN")}))
	}
	var buffer bytes.Buffer
	_, err = tree.WriteTo(&buffer)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "synthetic.mmdb")
	require.NoError(t, os.WriteFile(path, buffer.Bytes(), 0o600))
	return path
}

func TestASNOfflineEvidence(t *testing.T) {
	path := writeASNFixture(t, "GeoLite2-ASN")
	db, err := openASNDatabase(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.reader.Close()) })
	now := time.Now().UTC()
	for _, ip := range []string{"192.0.2.1", "::ffff:192.0.2.1", "2001:db8::1"} {
		result := db.lookup(ip, now)
		require.Equal(t, "found", result.State)
		require.Equal(t, uint32(64512), result.Number)
		require.Equal(t, "Synthetic ASN", result.Organization)
		require.Equal(t, now, result.ObservedAt)
		require.Equal(t, int64(1700000000), result.DatabaseBuiltAt.Unix())
		require.Len(t, result.DatabaseSHA256, 64)
	}
	require.Equal(t, "not_found", db.lookup("198.51.100.1", now).State)
	require.Equal(t, "error", db.lookup("invalid", now).State)
	// Replacing or truncating the source cannot mutate the active snapshot.
	require.NoError(t, os.WriteFile(path, []byte("invalid"), 0o600))
	require.Equal(t, "found", db.lookup("192.0.2.1", now).State)
	_, err = openASNDatabase(path)
	require.Error(t, err)
	_, err = openASNDatabase(writeASNFixture(t, "GeoLite2-Country"))
	require.Error(t, err)
	_, err = openASNDatabase(filepath.Join(t.TempDir(), "missing.mmdb"))
	require.Error(t, err)
}

func TestASNEnrichmentPreservesObservationAndCoverage(t *testing.T) {
	db, err := openASNDatabase(writeASNFixture(t, "GeoLite2-ASN"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.reader.Close()) })
	now := time.Now().UTC()
	history := models.RacingAnalysisHistory{StartedAt: now.Add(-time.Minute), Peers: map[string]models.RacingPeerHistory{
		"192.0.2.1:6881":    {IP: "192.0.2.1", Port: 6881},
		"198.51.100.1:6881": {IP: "198.51.100.1", Port: 6881},
	}}
	db.enrich(t.Context(), &history, now)
	history.Coverage(now)
	require.Equal(t, "available", history.ASNState)
	require.Zero(t, history.SuccessfulSamples, "ASN lookup does not improve Peer sampling coverage")
	require.Equal(t, 12, history.MissingSamples)
	require.Equal(t, "not_found", history.Peers["198.51.100.1:6881"].ASN.State)
	db.enrich(t.Context(), &history, now.Add(time.Hour))
	require.Equal(t, now, history.Peers["192.0.2.1:6881"].ASN.ObservedAt)
	history.Peers["203.0.113.1:6881"] = models.RacingPeerHistory{IP: "203.0.113.1"}
	history.Coverage(now)
	require.Equal(t, "partial", history.ASNState)
	oldPeer := history.Peers["192.0.2.1:6881"]
	oldPeer.ASN.DatabaseSHA256 = "previous-database"
	history.Peers["192.0.2.1:6881"] = oldPeer
	db.enrich(t.Context(), &history, now.Add(time.Minute))
	require.Equal(t, db.digest, history.Peers["192.0.2.1:6881"].ASN.DatabaseSHA256)
	require.Equal(t, now.Add(time.Minute), history.Peers["192.0.2.1:6881"].ASN.ObservedAt)
}
