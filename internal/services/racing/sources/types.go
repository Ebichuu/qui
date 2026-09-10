// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package sources parses individual source observations without downloading
// torrents, evaluating rules or granting any downloader mutation capability.
package sources

import (
	"context"
	"errors"
	"io"
	"time"
)

type Truth string

const (
	Unknown Truth = "unknown"
	Yes     Truth = "true"
	No      Truth = "false"
)

type Evidence struct {
	Value Truth  `json:"value"`
	Basis string `json:"basis,omitempty"`
}

// Item separates source identity from a reported (not yet verified) infohash.
// URLs are private transport data; API handlers must only expose PublicItem.
type PublicItem struct {
	SizeApproximate     bool       `json:"sizeApproximate"`
	TorrentID           string     `json:"torrentId"`
	EventKey            string     `json:"eventKey"`
	Title               string     `json:"title"`
	SizeBytes           *int64     `json:"sizeBytes,omitempty"`
	ReportedHash        string     `json:"reportedHash,omitempty"`
	PublishedAt         *time.Time `json:"publishedAt,omitempty"`
	OriginalPublishedAt *time.Time `json:"originalPublishedAt,omitempty"`
	FreeExpiresAt       *time.Time `json:"freeExpiresAt,omitempty"`
	Official            Evidence   `json:"official"`
	Free                Evidence   `json:"free"`
	Revival             Evidence   `json:"revival"`
}

type Item struct {
	PublicItem
	DownloadURL string `json:"-"`
	DetailsURL  string `json:"-"`
}

// Event keys are scoped by source ID in persistence and by site ID for correlation.
// A successful Emit commits one observation even if Parse later fails.
type Emit func(Item) error

type Capability struct {
	ID         string   `json:"id"`
	Site       string   `json:"site"`
	Kinds      []string `json:"kinds"`
	Pagination bool     `json:"pagination"`
	Official   bool     `json:"official"`
	Free       bool     `json:"free"`
	Revival    bool     `json:"revival"`
	Validation string   `json:"validation"`
}

func Capabilities() []Capability {
	return []Capability{
		{ID: "generic-rss", Site: "RSS 2.0", Kinds: []string{"rss"}, Validation: "synthetic_fixtures"},
		{ID: "chd", Site: "CHDBits", Kinds: []string{"rss", "web", "revival"}, Pagination: true, Official: true, Free: true, Revival: true, Validation: "synthetic_fixtures"},
		{ID: "mteam", Site: "M-Team", Kinds: []string{"rss"}, Free: true, Validation: "synthetic_fixtures"},
	}
}

var ErrUnsupported = errors.New("source adapter does not support this kind")
var ErrInvalidPage = errors.New("source response is not a recognized listing")
var ErrAuthentication = errors.New("source requires a valid login")

func supported(adapter, kind string) bool {
	for _, capability := range Capabilities() {
		if capability.ID == adapter {
			for _, allowed := range capability.Kinds {
				if allowed == kind {
					return true
				}
			}
		}
	}
	return false
}

func Parse(ctx context.Context, adapter, kind, address string, body io.Reader, emit Emit) error {
	if kind == "rss" {
		switch adapter {
		case "generic-rss", "chd", "mteam":
			return parseRSS(ctx, adapter, address, body, emit)
		}
	}
	if adapter == "chd" && (kind == "web" || kind == "revival") {
		return parseCHD(ctx, address, body, emit)
	}
	return ErrUnsupported
}
