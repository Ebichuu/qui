// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sources

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func syntheticMetainfo() ([]byte, []byte) {
	info := []byte("d6:lengthi1024e4:name14:Example Aurora12:piece lengthi16384e6:pieces20:abcdefghijklmnopqrste")
	payload := append([]byte("d4:info"), info...)
	payload = append(payload, 'e')
	return payload, info
}

func TestMetainfoUsesOriginalInfoBytes(t *testing.T) {
	data, info := syntheticMetainfo()
	result, err := ParseMetainfo(data)
	require.NoError(t, err)
	sum := sha1.Sum(info)
	require.Equal(t, hex.EncodeToString(sum[:]), result.HashV1)
	require.Empty(t, result.HashV2)
	require.Equal(t, "Example Aurora", result.Name)
	require.Equal(t, int64(1024), result.SizeBytes)
	_, err = ParseMetainfo([]byte("<html>Login required</html>"))
	require.ErrorIs(t, err, ErrInvalidMetainfo)
}

func TestSingleCandidateMetainfoFetch(t *testing.T) {
	data, _ := syntheticMetainfo()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "sid=synthetic-private", r.Header.Get("Cookie"))
		_, _ = w.Write(data)
	}))
	defer server.Close()
	fetcher := Fetcher{}
	result, raw, err := fetcher.FetchMetainfo(t.Context(), Request{SiteOrigin: server.URL, Cookie: "sid=synthetic-private"}, server.URL+"/download")
	require.NoError(t, err)
	require.Equal(t, data, raw)
	require.NotEmpty(t, result.HashV1)
}

func TestMetainfoV2AndInvalidLengths(t *testing.T) {
	info := []byte("d9:file treed14:Example Aurorad0:d6:lengthi0eeee12:meta versioni2e4:name14:Example Aurora12:piece lengthi16384ee")
	payload := append([]byte("d4:info"), info...)
	payload = append(payload, 'e')
	metadata, err := ParseMetainfo(payload)
	require.NoError(t, err)
	sum := sha256.Sum256(info)
	require.Equal(t, hex.EncodeToString(sum[:]), metadata.HashV2)
	require.Empty(t, metadata.HashV1)
	require.Zero(t, metadata.SizeBytes)
	raw, _ := syntheticMetainfo()
	for _, bad := range []string{
		strings.Replace(string(raw), "lengthi1024e", "lengthi-1e", 1),
		strings.Replace(string(raw), "piece lengthi16384e", "piece lengthi0e", 1),
		strings.Replace(string(raw), "lengthi1024e", "lengthi999999999e", 1),
	} {
		_, err := ParseMetainfo([]byte(bad))
		require.ErrorIs(t, err, ErrInvalidMetainfo)
	}
}
