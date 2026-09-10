// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sources

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const rssExample = `<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel><item><title>Example Aurora 1080p-CHD</title><link>/details.php?id=42&amp;token=private-example</link><guid>0123456789012345678901234567890123456789</guid><pubDate>Thu, 10 Sep 2026 10:00:00 +0800</pubDate><enclosure url="/download.php?id=42&amp;passkey=private-example" length="1024"/><torznab:attr name="downloadvolumefactor" value="0"/></item>`

func collect(t *testing.T, adapter, kind, address, body string) []Item {
	t.Helper()
	var items []Item
	require.NoError(t, Parse(context.Background(), adapter, kind, address, strings.NewReader(body), func(item Item) error { items = append(items, item); return nil }))
	return items
}

func TestRSSIncrementalDelivery(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	emitted := make(chan Item, 1)
	done := make(chan error, 1)
	go func() {
		done <- Parse(context.Background(), "chd", "rss", "https://tracker.invalid/feed", reader, func(item Item) error { emitted <- item; return nil })
	}()
	_, err := writer.Write([]byte(rssExample))
	require.NoError(t, err)
	select {
	case item := <-emitted:
		require.Equal(t, "42", item.TorrentID)
		require.Equal(t, Yes, item.Official.Value)
		require.Equal(t, Yes, item.Free.Value)
		require.Equal(t, No, item.Revival.Value)
		require.Empty(t, item.ReportedHash, "GUID must not become a verified or reported infohash")
		require.Equal(t, int64(1024), *item.SizeBytes)
		require.Equal(t, time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC), *item.PublishedAt)
		public, err := json.Marshal(item.PublicItem)
		require.NoError(t, err)
		require.NotContains(t, string(public), "private-example")
	case <-time.After(2 * time.Second):
		t.Fatal("complete item waited for trailing feed")
	}
	_, err = writer.Write([]byte(`<item><title>broken</channel>`))
	require.NoError(t, err)
	require.ErrorIs(t, <-done, ErrInvalidPage)
}

func TestRSSAdapterEvidence(t *testing.T) {
	cases := []struct {
		name, adapter, body string
		free                Truth
		id, hash            string
	}{
		{"mteam path", "mteam", `<item><title>Example Harbor</title><link>https://tracker.invalid/detail/73</link><enclosure url="/dl/73"/><attr name="infohash" value="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"/><attr name="downloadvolumefactor" value="1"/></item>`, No, "73", strings.Repeat("a", 40)},
		{"missing evidence", "generic-rss", `<item><title>Example Harbor</title><link>/details.php?id=private-example</link><guid>secret-passkey</guid><enclosure url="/dl" length="bad"/></item>`, Unknown, "", ""},
		{"invalid factor", "generic-rss", `<item><title>Example Harbor</title><enclosure url="/dl"/><attr name="downloadvolumefactor" value="+Inf"/></item>`, Unknown, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := collect(t, tc.adapter, "rss", "https://tracker.invalid/feed", `<rss><channel>`+tc.body+`</channel></rss>`)
			require.Len(t, items, 1)
			require.Equal(t, tc.free, items[0].Free.Value)
			require.Equal(t, tc.id, items[0].TorrentID)
			require.Equal(t, tc.hash, items[0].ReportedHash)
			require.Equal(t, Unknown, items[0].Official.Value)
			require.NotContains(t, items[0].EventKey, "private-example")
			require.NotContains(t, items[0].EventKey, "secret-passkey")
		})
	}
}

func chdExample(extra, title string) string {
	return `<html><table class="torrents"><tr><th>Listing</th></tr><tr><td>Category</td><td><a href="details.php?id=42&amp;hit=1" title="` + title + `"><b>Example Aurora...</b></a>` + extra + `<a href="download.php?id=42&amp;passkey=private-example">Download</a></td><td>1.5 GiB</td><td class="rowfollow nowrap"><span title="2026-09-01 10:00:00">old</span></td><td><span title="2099-01-01 10:00:00">Unrelated tooltip</span></td></tr></table></html>`
}

func TestCHDLongTitleAndRevival(t *testing.T) {
	title := "Example Aurora Complete Long Title 2160p-CHD"
	body := chdExample(`<span>官方</span><span class="free"><span title="2026-09-17 10:00:00">Free</span></span>`, title)
	items := collect(t, "chd", "revival", "https://tracker.invalid/renewtorrents.php", body)
	require.Len(t, items, 1)
	item := items[0]
	require.Equal(t, title, item.Title)
	require.Equal(t, Yes, item.Official.Value)
	require.Equal(t, Yes, item.Free.Value)
	require.Equal(t, Yes, item.Revival.Value)
	require.Equal(t, time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC), *item.PublishedAt)
	require.Equal(t, time.Date(2026, 9, 17, 2, 0, 0, 0, time.UTC), *item.FreeExpiresAt)
	require.Equal(t, "torrent:42:revival:"+strconv.FormatInt(item.PublishedAt.Unix(), 10), item.EventKey)
	require.Equal(t, int64(1610612736), *item.SizeBytes)
	require.Empty(t, item.ReportedHash)
	normal := collect(t, "chd", "web", "https://tracker.invalid/torrents.php", body)
	require.NotEqual(t, normal[0].EventKey, item.EventKey)
	require.Equal(t, No, normal[0].Revival.Value)
}

func TestCHDMissingEvidenceAndLogin(t *testing.T) {
	body := chdExample("", "Example Aurora Complete Long Title")
	items := collect(t, "chd", "revival", "https://tracker.invalid/renewtorrents.php", body)
	require.Equal(t, Unknown, items[0].Official.Value)
	require.Equal(t, Unknown, items[0].Free.Value)
	require.Nil(t, items[0].PublishedAt)
	require.Nil(t, items[0].FreeExpiresAt)
	for _, tc := range []struct {
		body string
		err  error
	}{
		{`<html><form action="takelogin.php"></form></html>`, ErrAuthentication},
		{`<html>Maintenance</html>`, ErrInvalidPage},
	} {
		require.ErrorIs(t, Parse(context.Background(), "chd", "web", "https://tracker.invalid/torrents.php", strings.NewReader(tc.body), func(Item) error { t.Fatal("unexpected item"); return nil }), tc.err)
	}
}

func TestPagesPreservesPrivateParameters(t *testing.T) {
	pages, err := Pages("chd", "web", "https://tracker.invalid/torrents.php?passkey=private-example&page=9", 3)
	require.NoError(t, err)
	require.Len(t, pages, 3)
	for index, page := range pages {
		parsed, err := url.Parse(page)
		require.NoError(t, err)
		require.Equal(t, "private-example", parsed.Query().Get("passkey"))
		require.Equal(t, "4", parsed.Query().Get("sort"))
		if index == 0 {
			require.Empty(t, parsed.Query().Get("page"))
		} else {
			require.Equal(t, strconv.Itoa(index), parsed.Query().Get("page"))
		}
	}
	pages, err = Pages("chd", "revival", "https://tracker.invalid/renewtorrents.php", 5)
	require.NoError(t, err)
	require.Len(t, pages, 1)
}

func TestParserCancellationAndEmissionFailure(t *testing.T) {
	sentinel := errors.New("persistence unavailable")
	require.ErrorIs(t, Parse(context.Background(), "chd", "rss", "https://tracker.invalid/rss", strings.NewReader(rssExample+`</channel></rss>`), func(Item) error { return sentinel }), sentinel)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, Parse(ctx, "chd", "rss", "https://tracker.invalid/rss", strings.NewReader(rssExample), func(Item) error { return nil }), context.Canceled)
	require.ErrorIs(t, Parse(context.Background(), "mteam", "web", "https://tracker.invalid/rss", strings.NewReader(""), nil), ErrUnsupported)
}
