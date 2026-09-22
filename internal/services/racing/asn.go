// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package racing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/netip"
	"os"
	"time"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/autobrr/qui/internal/models"
)

type asnDatabase struct {
	reader *maxminddb.Reader
	digest string
}

// SetASNDatabasePath configures the analysis worker before Start. Changing the
// database requires a restart; active readers always use an immutable snapshot.
func (s *Service) SetASNDatabasePath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asnDatabasePath = path
}

func openASNDatabase(path string) (*asnDatabase, error) {
	const maxBytes = 64 << 20
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, errors.New("ASN database must be a regular file of at most 64 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, errors.New("ASN database exceeds 64 MiB")
	}
	reader, err := maxminddb.OpenBytes(data)
	if err != nil {
		return nil, err
	}
	if reader.Metadata.DatabaseType != "GeoLite2-ASN" {
		_ = reader.Close()
		return nil, errors.New("expected a GeoLite2-ASN database")
	}
	if err := reader.Verify(); err != nil {
		_ = reader.Close()
		return nil, err
	}
	digest := sha256.Sum256(data)
	return &asnDatabase{reader: reader, digest: hex.EncodeToString(digest[:])}, nil
}

func (db *asnDatabase) lookup(ip string, now time.Time) *models.RacingASNObservation {
	observation := &models.RacingASNObservation{State: "error", ObservedAt: now, DatabaseBuiltAt: db.reader.Metadata.BuildTime(), DatabaseSHA256: db.digest}
	address, err := netip.ParseAddr(ip)
	if err != nil || address.Zone() != "" {
		return observation
	}
	result := db.reader.Lookup(address.Unmap())
	var record struct {
		Number       uint32 `maxminddb:"autonomous_system_number"`
		Organization string `maxminddb:"autonomous_system_organization,maxsize:1024"`
	}
	if err := result.Decode(&record); err != nil {
		return observation
	}
	if !result.Found() {
		observation.State = "not_found"
		return observation
	}
	if record.Number == 0 {
		return observation
	}
	observation.State = "found"
	observation.Number, observation.Organization = record.Number, record.Organization
	observation.Network = result.Prefix().String()
	return observation
}

// Enrichment stays in the sole analysis worker and only touches bounded history.
// Missing records are cached for this database version, not retried every sample.
func (db *asnDatabase) enrich(ctx context.Context, history *models.RacingAnalysisHistory, now time.Time) {
	if db == nil {
		return
	}
	for endpoint, peer := range history.Peers {
		if ctx.Err() != nil {
			return
		}
		if peer.ASN != nil && peer.ASN.DatabaseSHA256 == db.digest {
			continue
		}
		peer.ASN = db.lookup(peer.IP, now)
		history.Peers[endpoint] = peer
	}
}
