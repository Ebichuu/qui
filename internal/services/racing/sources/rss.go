// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var torrentIDPattern = regexp.MustCompile(`^[0-9]+$`)

var infoHashPattern = regexp.MustCompile(`(?i)^[0-9a-f]{40}$`)
var mteamIDPattern = regexp.MustCompile(`/([0-9]+)(?:/|$)`)
var officialCHDPattern = regexp.MustCompile(`(?i)(?:-(?:CHD|CHDBits|CHDWEB|CHDTV|CHDPAD|CHDHKTV|SGNB|OneHD|blucook|KAN|JKCT|BMDru|Destiny|SP)|@CHDBits)(?:\s|$)`)

type rssItem struct {
	Title     string `xml:"title"`
	Link      string `xml:"link"`
	GUID      string `xml:"guid"`
	PubDate   string `xml:"pubDate"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length string `xml:"length,attr"`
	} `xml:"enclosure"`
	Attrs []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	} `xml:"attr"`
}

// Decode one item at a time: a slow trailing item or malformed later page never
// holds already complete observations behind a whole-feed parsing barrier.
func parseRSS(ctx context.Context, adapter, address string, body io.Reader, emit Emit) error {
	decoder := xml.NewDecoder(body)
	found := false
	channel := false
	seenChannel := false
	depth := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if !found || !seenChannel || depth != 0 {
				return ErrInvalidPage
			}
			return nil
		}
		if err != nil {
			return ErrInvalidPage
		}
		if end, ok := token.(xml.EndElement); ok {
			if depth == 2 && end.Name.Local == "channel" {
				channel = false
			}
			depth--
			continue
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		depth++
		if depth == 1 {
			if found || start.Name.Local != "rss" {
				return ErrInvalidPage
			}
			found = true
		}
		if depth == 2 && start.Name.Local == "channel" {
			channel = true
			seenChannel = true
		}
		if start.Name.Local != "item" || !channel || depth != 3 {
			continue
		}
		var entry rssItem
		if err := decoder.DecodeElement(&entry, &start); err != nil {
			return ErrInvalidPage
		}
		depth-- // DecodeElement consumed this item's closing token.
		item, ok := convertRSS(adapter, address, entry)
		if !ok {
			continue
		}
		if err := emit(item); err != nil {
			return err
		}
	}
}

func convertRSS(adapter, address string, entry rssItem) (Item, bool) {
	item := Item{PublicItem: PublicItem{Title: strings.TrimSpace(entry.Title), Official: Evidence{Value: Unknown}, Free: Evidence{Value: Unknown}, Revival: Evidence{Value: Unknown}}}
	item.DownloadURL = resolveHTTP(address, entry.Enclosure.URL)
	item.DetailsURL = resolveHTTP(address, entry.Link)
	if item.Title == "" || item.DownloadURL == "" {
		return Item{}, false
	}
	if size, err := strconv.ParseInt(entry.Enclosure.Length, 10, 64); err == nil && size >= 0 {
		item.SizeBytes = &size
	}
	if parsed, err := url.Parse(item.DetailsURL); err == nil {
		item.TorrentID = parsed.Query().Get("id")
		if adapter == "mteam" {
			if match := mteamIDPattern.FindStringSubmatch(parsed.Path); len(match) > 1 {
				item.TorrentID = match[1]
			}
		}
	}
	if !torrentIDPattern.MatchString(item.TorrentID) {
		item.TorrentID = ""
	}
	for _, attribute := range entry.Attrs {
		switch strings.ToLower(attribute.Name) {
		case "infohash":
			if infoHashPattern.MatchString(attribute.Value) {
				item.ReportedHash = strings.ToLower(attribute.Value)
			}
		case "size":
			if size, err := strconv.ParseInt(attribute.Value, 10, 64); err == nil && size >= 0 {
				item.SizeBytes = &size
			}
		case "downloadvolumefactor":
			if factor, err := strconv.ParseFloat(attribute.Value, 64); err == nil && !math.IsInf(factor, 0) && factor >= 0 {
				value := No
				if factor == 0 {
					value = Yes
				}
				item.Free = Evidence{Value: value, Basis: "rss_download_volume_factor"}
			}
		}
	}
	item.PublishedAt = parseRSSDate(entry.PubDate)
	item.OriginalPublishedAt = item.PublishedAt
	if adapter == "chd" && officialCHDPattern.MatchString(item.Title) {
		item.Official = Evidence{Value: Yes, Basis: "chd_title_suffix"}
	}
	if adapter == "chd" || adapter == "mteam" {
		item.Revival = Evidence{Value: No, Basis: "ordinary_rss"}
	}
	if item.TorrentID != "" {
		item.EventKey = "torrent:" + item.TorrentID + ":published"
	} else {
		// GUID is only a source identifier; even a hex-shaped GUID is not proof
		// of an infohash. Hash it so an embedded passkey cannot enter public IDs.
		identity := strings.TrimSpace(entry.GUID)
		if identity == "" {
			identity = item.DetailsURL
		}
		if identity == "" {
			identity = item.DownloadURL
		}
		sum := sha256.Sum256([]byte(identity))
		item.EventKey = "source:" + hex.EncodeToString(sum[:])
	}
	return item, true
}

func parseRSSDate(raw string) *time.Time {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC3339} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			at := parsed.UTC()
			return &at
		}
	}
	return nil
}

func resolveHTTP(base, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	parent, err := url.Parse(base)
	if err != nil {
		return ""
	}
	child, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	result := parent.ResolveReference(child)
	if result.Hostname() == "" || (result.Scheme != "http" && result.Scheme != "https") {
		return ""
	}
	result.Fragment = ""
	return result.String()
}

func Pages(adapter, kind, address string, count int) ([]string, error) {
	if !supported(adapter, kind) {
		return nil, ErrUnsupported
	}
	if count < 1 || count > 5 {
		return nil, errors.New("page count must be between 1 and 5")
	}
	if adapter != "chd" || kind == "rss" {
		return []string{address}, nil
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return nil, ErrInvalidPage
	}
	if kind == "revival" && strings.EqualFold(parsed.Path, "/renewtorrents.php") {
		return []string{address}, nil
	}
	if kind != "web" || !strings.EqualFold(parsed.Path, "/torrents.php") {
		return nil, ErrUnsupported
	}
	values := parsed.Query()
	for key, value := range map[string]string{"allsec": "1", "inclbookmarked": "0", "incldead": "0", "spstate": "0", "sort": "4", "type": "desc"} {
		values.Set(key, value)
	}
	pages := make([]string, 0, count)
	for page := range count {
		values.Del("page")
		if page > 0 {
			values.Set("page", strconv.Itoa(page))
		}
		parsed.RawQuery = values.Encode()
		pages = append(pages, parsed.String())
	}
	return pages, nil
}
