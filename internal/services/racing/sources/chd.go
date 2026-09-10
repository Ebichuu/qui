// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sources

import (
	"context"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var sizePattern = regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)\s*(B|KiB|MiB|GiB|TiB|KB|MB|GB|TB)$`)
var chdZone = time.FixedZone("CHD", 8*60*60)

func attr(node *html.Node, name string) string {
	for _, a := range node.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}
func hasClass(node *html.Node, class string) bool {
	for _, item := range strings.Fields(attr(node, "class")) {
		if item == class {
			return true
		}
	}
	return false
}
func nodeText(node *html.Node) string {
	var result strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.TextNode {
			result.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(node)
	return strings.Join(strings.Fields(result.String()), " ")
}
func findNode(node *html.Node, match func(*html.Node) bool) *html.Node {
	if match(node) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findNode(child, match); found != nil {
			return found
		}
	}
	return nil
}
func parseCHDTime(raw string) *time.Time {
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", raw, chdZone); err == nil {
		utc := parsed.UTC()
		return &utc
	}
	return nil
}
func parseSize(raw string) *int64 {
	match := sizePattern.FindStringSubmatch(strings.TrimSpace(raw))
	if len(match) != 3 {
		return nil
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return nil
	}
	multipliers := map[string]float64{"b": 1, "kib": 1024, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40, "kb": 1000, "mb": 1e6, "gb": 1e9, "tb": 1e12}
	value *= multipliers[strings.ToLower(match[2])]
	if value < 0 || value >= math.MaxInt64 {
		return nil
	}
	bytes := int64(value)
	return &bytes
}

func parseCHD(ctx context.Context, address string, body io.Reader, emit Emit) error {
	document, err := html.Parse(body)
	if err != nil {
		return ErrInvalidPage
	}
	if findNode(document, func(n *html.Node) bool { return n.Data == "form" && strings.Contains(attr(n, "action"), "takelogin") }) != nil {
		return ErrAuthentication
	}
	table := findNode(document, func(n *html.Node) bool { return n.Data == "table" && hasClass(n, "torrents") })
	if table == nil {
		return ErrInvalidPage
	}
	page, err := url.Parse(address)
	if err != nil {
		return ErrInvalidPage
	}
	revival := strings.EqualFold(page.Path, "/renewtorrents.php")
	var visit func(*html.Node) error
	visit = func(node *html.Node) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if node.Type == html.ElementNode && node.Data == "tr" {
			if item, ok := parseCHDRow(node, address, revival); ok {
				return emit(item)
			}
			return nil
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(table)
}

func parseCHDRow(row *html.Node, address string, revival bool) (Item, bool) {
	titleAnchor := findNode(row, func(n *html.Node) bool {
		return n.Data == "a" && strings.Contains(attr(n, "href"), "details.php?id=") && findNode(n, func(b *html.Node) bool { return b.Data == "b" }) != nil
	})
	downloadAnchor := findNode(row, func(n *html.Node) bool { return n.Data == "a" && strings.Contains(attr(n, "href"), "download.php?id=") })
	if titleAnchor == nil || downloadAnchor == nil {
		return Item{}, false
	}
	item := Item{PublicItem: PublicItem{Official: Evidence{Value: Unknown}, Free: Evidence{Value: Unknown}, Revival: Evidence{Value: No, Basis: "chd_list_path"}}}
	item.DetailsURL = resolveHTTP(address, attr(titleAnchor, "href"))
	item.DownloadURL = resolveHTTP(address, attr(downloadAnchor, "href"))
	parsed, err := url.Parse(item.DetailsURL)
	if err != nil {
		return Item{}, false
	}
	item.TorrentID = parsed.Query().Get("id")
	if !torrentIDPattern.MatchString(item.TorrentID) || item.DownloadURL == "" {
		return Item{}, false
	}
	title := findNode(titleAnchor, func(n *html.Node) bool { return n.Data == "b" })
	item.Title = nodeText(title)
	for _, full := range []string{attr(titleAnchor, "title"), attr(title, "title")} {
		if len(strings.TrimSpace(full)) > len(item.Title) {
			item.Title = strings.TrimSpace(full)
		}
	}
	if item.Title == "" {
		return Item{}, false
	}
	for child := row.FirstChild; child != nil; child = child.NextSibling {
		if child.Data != "td" {
			continue
		}
		if size := parseSize(nodeText(child)); size != nil {
			item.SizeBytes = size
		}
		if hasClass(child, "rowfollow") && hasClass(child, "nowrap") {
			if stamp := findNode(child, func(n *html.Node) bool { return n.Data == "span" && attr(n, "title") != "" }); stamp != nil {
				item.OriginalPublishedAt = parseCHDTime(attr(stamp, "title"))
			}
		}
	}
	if findNode(row, func(n *html.Node) bool { return n.Type == html.ElementNode && nodeText(n) == "官方" }) != nil {
		item.Official = Evidence{Value: Yes, Basis: "chd_list_tag"}
	}
	if officialCHDPattern.MatchString(item.Title) {
		item.Official = Evidence{Value: Yes, Basis: "chd_title_suffix"}
	}
	free := findNode(row, func(n *html.Node) bool { return hasClass(n, "free") || hasClass(n, "twoupfree") })
	if free != nil {
		item.Free = Evidence{Value: Yes, Basis: "chd_list_marker"}
		if stamp := findNode(free, func(n *html.Node) bool { return attr(n, "title") != "" && parseCHDTime(attr(n, "title")) != nil }); stamp != nil {
			item.FreeExpiresAt = parseCHDTime(attr(stamp, "title"))
		}
	}
	item.PublishedAt = item.OriginalPublishedAt
	item.EventKey = "torrent:" + item.TorrentID + ":published"
	if revival {
		item.Revival = Evidence{Value: Yes, Basis: "chd_revival_path"}
		item.PublishedAt = nil
		item.EventKey = "torrent:" + item.TorrentID + ":revival:unknown"
		// This seven-day rule is specific to CHD's revival listing. Only a time
		// inside the free marker is evidence, never an arbitrary row tooltip.
		if item.FreeExpiresAt != nil && item.OriginalPublishedAt != nil && item.FreeExpiresAt.After(*item.OriginalPublishedAt) {
			at := item.FreeExpiresAt.Add(-7 * 24 * time.Hour)
			item.PublishedAt = &at
			item.EventKey = "torrent:" + item.TorrentID + ":revival:" + strconv.FormatInt(at.Unix(), 10)
		}
	}
	return item, true
}
