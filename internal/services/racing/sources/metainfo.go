// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sources

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/autobrr/go-torrent/metainfo"
)

type VerifiedMetadata struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	HashV1    string `json:"hashV1,omitempty"`
	HashV2    string `json:"hashV2,omitempty"`
}

var ErrInvalidMetainfo = errors.New("source returned invalid torrent metadata")

func ParseMetainfo(data []byte) (VerifiedMetadata, error) {
	result := VerifiedMetadata{}
	meta, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return result, ErrInvalidMetainfo
	}
	info, err := meta.UnmarshalInfo()
	if err != nil {
		return result, ErrInvalidMetainfo
	}
	if info.PieceLength <= 0 || (info.MetaVersion != 0 && info.MetaVersion != 1 && info.MetaVersion != 2) {
		return result, ErrInvalidMetainfo
	}
	var total int64
	for _, file := range info.UpvertedFiles() {
		if file.Length < 0 || total > math.MaxInt64-file.Length {
			return result, ErrInvalidMetainfo
		}
		total += file.Length
	}
	if info.HasV1() {
		if len(info.Pieces)%20 != 0 {
			return result, ErrInvalidMetainfo
		}
		if !info.HasV2() {
			pieces := total / info.PieceLength
			if total%info.PieceLength != 0 {
				pieces++
			}
			if pieces != int64(len(info.Pieces)/20) {
				return result, ErrInvalidMetainfo
			}
		}
	}
	magnet, err := meta.Magnet()
	if err != nil {
		return result, ErrInvalidMetainfo
	}
	result.Name = info.BestName()
	result.SizeBytes = total
	if !magnet.InfoHash.IsZero() {
		result.HashV1 = magnet.InfoHash.HexString()
	}
	if !magnet.InfoHashV2.IsZero() {
		result.HashV2 = magnet.InfoHashV2.HexString()
	}
	if result.Name == "" || result.SizeBytes < 0 || (result.HashV1 == "" && result.HashV2 == "") {
		return VerifiedMetadata{}, ErrInvalidMetainfo
	}
	return result, nil
}

// FetchMetainfo is an explicit, single-candidate request. Listing discovery
// never invokes it for a batch or merely because promotion expiry is absent.
func (f *Fetcher) FetchMetainfo(ctx context.Context, input Request, address string) (VerifiedMetadata, []byte, error) {
	if input.Budget != nil {
		if err := input.Budget.Wait(ctx, input.RequestInterval); err != nil {
			return VerifiedMetadata{}, nil, err
		}
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, address, nil)
	if err != nil {
		return VerifiedMetadata{}, nil, ErrRequest
	}
	req.Header.Set("User-Agent", "qui-source-observer")
	if input.Cookie != "" && sameOrigin(address, input.SiteOrigin) {
		req.Header.Set("Cookie", input.Cookie)
	}
	client := f.client()
	response, err := client.Do(req)
	if err != nil {
		return VerifiedMetadata{}, nil, ErrRequest
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return VerifiedMetadata{}, nil, ErrAuthentication
	}
	if response.StatusCode != http.StatusOK {
		return VerifiedMetadata{}, nil, ErrRequest
	}
	limit := f.MaxBytes
	if limit <= 0 {
		limit = 8 << 20
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return VerifiedMetadata{}, nil, ErrRequest
	}
	if int64(len(data)) > limit {
		return VerifiedMetadata{}, nil, ErrResponseLimit
	}
	result, err := ParseMetainfo(data)
	if err != nil {
		return VerifiedMetadata{}, nil, err
	}
	return result, data, nil
}
