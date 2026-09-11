#!/usr/bin/env python3
"""C09: process-level fault checks against loopback fixtures, never real trackers/qB."""
from datetime import datetime, timedelta, timezone
import hashlib
import http.server
import json
import math
import socket
import threading
import time
import urllib.parse

from racing_smoke import App, until

ANNOUNCE = "https://tracker.invalid/announce?key=synthetic"
MIB = 1024 * 1024
# Registered before running: conservative CI ceilings, not production claims.
LIMITS = {"discovery": 15, "metadata": 35, "internal": 10, "confirmation": 15, "total": 60}


def torrent(number, size=MIB):
    name = f"Synthetic Aurora {number}"
    info = f"d6:lengthi{size}e4:name{len(name)}:{name}12:piece lengthi{size}e6:pieces20:abcdefghijklmnopqrste".encode()
    return dict(id=number, size=size, info=info, raw=f"d8:announce{len(ANNOUNCE)}:{ANNOUNCE}4:info".encode()+info+b"e", hash=hashlib.sha1(info).hexdigest())


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def respond(self, value, status=200):
        data = value if isinstance(value, bytes) else (value if isinstance(value, str) else json.dumps(value)).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        try:
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_GET(self):
        f = self.server.fixture
        parsed = urllib.parse.urlparse(self.path)
        query = urllib.parse.parse_qs(parsed.query)
        path = parsed.path
        if path in ("/rss", "/torrents.php"):
            key = path + ("?page=1" if query.get("page") == ["1"] else "")
            f.wait(key)
            with f.lock:
                items = list(f.feeds.get(path, [])) if query.get("page") != ["1"] else []
            if path == "/rss":
                stamp = f.published.strftime("%a, %d %b %Y %H:%M:%S +0000")
                entries = [f'<item><title>Synthetic Aurora {t["id"]}-CHD</title><link>/details.php?id={t["id"]}</link><pubDate>{stamp}</pubDate><enclosure url="/download?id={t["id"]}" length="{t["size"]}"/><attr name="downloadvolumefactor" value="0"/></item>' for t in items]
                return self.respond('<rss><channel>'+''.join(entries)+'</channel></rss>')
            stamp = f.published.astimezone(timezone(timedelta(hours=8))).strftime("%Y-%m-%d %H:%M:%S")
            entries = [f'<tr><td><a href="details.php?id={t["id"]}" title="Synthetic Aurora {t["id"]}-CHD"><b>Synthetic Aurora</b></a><span>官方</span><a href="download.php?id={t["id"]}">Download</a></td><td>{t["size"] // MIB} MiB</td><td class="rowfollow nowrap"><span title="{stamp}">now</span></td></tr>' for t in items]
            return self.respond('<table class="torrents">'+''.join(entries)+'</table>')
        if path in ("/download", "/download.php"):
            number = int(query["id"][0])
            f.wait(f"/download?id={number}")
            if number in f.failed_metadata:
                f.failed_seen.add(number)
                return self.respond("synthetic metadata failure", 503)
            with f.lock:
                f.metadata_at.setdefault(number, time.time())
            return self.respond(f.items[number]["raw"])
        if path.endswith("/app/version"):
            return self.respond("v5.1.2")
        if path.endswith("/app/webapiVersion"):
            return self.respond("2.11.4")
        if path.endswith("/app/buildInfo"):
            return self.respond({"qt": "6.8", "libtorrent": "2.0.11", "bitness": 64})
        if path.endswith("/app/preferences"):
            return self.respond({"save_path": "/synthetic/disk", "temp_path_enabled": False})
        if path.endswith("/torrents/trackers"):
            f.wait("trackers")
            return self.respond([{"url": ANNOUNCE, "status": 2}])
        if path.endswith("/sync/maindata"):
            f.wait("sync")
            with f.lock:
                f.rid += 1
                data = {"rid": f.rid, "full_update": True, "torrents": dict(f.tasks), "categories": {}, "tags": [], "server_state": {"connection_status": "connected", "free_space_on_disk": f.free, "dl_info_speed": 0, "up_info_speed": 0}}
            return self.respond(data)
        return self.respond([])

    def do_POST(self):
        f = self.server.fixture
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        if self.path.endswith("/auth/login"):
            return self.respond("Ok.")
        if self.path == "/hook":
            with f.lock:
                f.notifications += 1
            f.wait("notifications")
            return self.respond("synthetic notification failure", 503)
        if self.path.endswith("/torrents/add"):
            matching = [t for t in f.items.values() if t["info"] in body]
            if len(matching) != 1:
                f.errors.append("add did not contain exactly one verified torrent")
                return self.respond("invalid synthetic add", 400)
            item = matching[0]
            with f.lock:
                f.adds.append(item["id"])
            if f.add_mode == "disconnect":
                self.connection.shutdown(socket.SHUT_RDWR)
                self.connection.close()
                return
            f.wait("add")
            time.sleep(0.05)
            f.show(item)
            return self.respond({"success_count": 1, "pending_count": 0, "failure_count": 0, "added_torrent_ids": [item["hash"]]})
        f.errors.append(self.path)
        return self.respond("unexpected write", 400)


class Fixture:
    def __init__(self, *items, free=1000*MIB):
        self.items = {t["id"]: t for t in items}
        self.feeds, self.tasks, self.gates, self.started = {}, {}, {}, {}
        self.metadata_at, self.published_at, self.started_at = {}, {}, {}
        self.failed_metadata, self.failed_seen = set(), set()
        self.adds, self.errors = [], []
        self.add_mode, self.rid, self.notifications, self.free = "ok", 0, 0, free
        self.published = datetime.now(timezone.utc)
        self.lock = threading.Lock()
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.fixture = self
        self.origin = f"http://127.0.0.1:{self.server.server_port}"

    def block(self, key):
        self.gates[key], self.started[key] = threading.Event(), threading.Event()

    def wait(self, key):
        if key in self.gates:
            self.started_at.setdefault(key, time.monotonic())
            self.started[key].set()
            self.gates[key].wait(90)

    def publish(self, path, *items):
        with self.lock:
            self.feeds[path] = list(items)
            for item in items:
                self.published_at.setdefault(item["id"], time.time())

    def show(self, item):
        with self.lock:
            self.tasks[item["hash"]] = dict(hash=item["hash"], infohash_v1=item["hash"], name=f'Synthetic Aurora {item["id"]}-CHD', save_path="/synthetic/disk", size=item["size"], total_size=item["size"], amount_left=item["size"]-1, completed=1, downloaded=1, uploaded=0, dlspeed=1, state="downloading")

    def __enter__(self):
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        return self

    def __exit__(self, *_args):
        for gate in self.gates.values():
            gate.set()
        self.server.shutdown()
        self.server.server_close()


def downloader(app, fixture, pool=None):
    instance = app.request("/api/instances", dict(name="Synthetic C09 downloader", host=fixture.origin, username="synthetic", password="synthetic"), expected=201)["id"]
    if pool is None:
        pool = app.request("/api/racing/storage-pools", dict(name="Synthetic C09 disk"), expected=201)["id"]
    app.request("/api/racing/path-mappings", dict(instanceId=instance, storagePoolId=pool, path="/synthetic/disk"), expected=201)
    app.request(f"/api/racing/reception-policies/{instance}", dict(instanceId=instance, enabled=True, maxConcurrentAdds=2, maxActiveDownloads=50, minFreeBytes=0, savePath="/synthetic/disk", category="", autoTMM=False, startPaused=False), method="PUT", expected=204)
    return instance, pool


def sources(app, fixture, instance, kinds=("rss",), page_count=1, interval=2):
    site = app.request("/api/racing/sites", dict(name="Synthetic C09 site", baseUrl=fixture.origin, enabled=True, trackerHosts=["tracker.invalid"], requestIntervalSeconds=1), expected=201)["id"]
    ids = []
    for kind in kinds:
        path = "/rss" if kind == "rss" else "/torrents.php"
        ids.append(app.request("/api/racing/sources", dict(name=f"Synthetic {kind}", siteId=site, kind=kind, adapter="chd", enabled=True, intervalSeconds=interval, initialLookbackSeconds=300, pageCount=page_count, url=fixture.origin+path), expected=201)["id"])
    app.request("/api/racing/rules", dict(name="Synthetic official rule", enabled=True, sourceIds=ids, acceptKinds=["official"], receiveWindowSeconds=900, targetInstanceId=instance), expected=201)


def intents(app):
    return app.request("/api/racing/add-intents")["items"]


def confirmed(app, count=1):
    try:
        return until(lambda: (rows if len(rows := [i for i in intents(app) if i["state"] == "confirmed"]) == count else None), f"expected {count} confirmed intents", 60)
    except RuntimeError:
        print("INTENT DIAGNOSTICS", json.dumps(intents(app)), flush=True)
        print("CANDIDATE DIAGNOSTICS", json.dumps(app.request("/api/racing/candidates")), flush=True)
        raise


def assert_clean(*fixtures):
    for fixture in fixtures:
        assert not fixture.errors, fixture.errors
        assert len(fixture.adds) == len(set(fixture.adds)), "duplicate add request"


def dual_source(slow):
    item = torrent(42)
    with App() as app, Fixture(item) as f:
        f.block(slow)
        if slow == "/rss":
            f.block("/torrents.php?page=1")
        instance, _ = downloader(app, f)
        sources(app, f, instance, ("rss", "web"), page_count=2)
        f.publish("/rss", item)
        f.publish("/torrents.php", item)
        until(lambda: f.started[slow].is_set(), "slow source never started")
        confirmed(app)
        assert f.adds == [42] and not f.gates[slow].is_set()
        assert time.monotonic() - f.started_at[slow] < 20, "fast path waited for slow source timeout"
        if slow == "/rss":
            assert f.started["/torrents.php?page=1"].is_set(), "later page not exercised"
        for gate in f.gates.values():
            gate.set()
        until(lambda: len(app.request("/api/racing/discoveries")["items"]) >= 2, "second source did not arrive")
        time.sleep(2)
        assert len(intents(app)) == 1
        assert_clean(f)
    print(f"PASS dual source: {slow} held; other source adds before release; merged once", flush=True)


def metadata_isolation():
    slow, good, failed = torrent(1), torrent(2), torrent(3)
    with App() as app, Fixture(slow, good, failed) as f:
        f.block("/download?id=1")
        f.failed_metadata.add(3)
        instance, _ = downloader(app, f)
        sources(app, f, instance)
        f.publish("/rss", slow, good, failed)
        until(lambda: f.started["/download?id=1"].is_set(), "slow metadata not exercised")
        confirmed(app)
        assert f.adds == [2] and not f.gates["/download?id=1"].is_set()
        assert time.monotonic() - f.started_at["/download?id=1"] < 15, "healthy candidate waited for metadata timeout"
        until(lambda: 3 in f.failed_seen, "metadata error was not exercised")
        assert_clean(f)
    print("PASS metadata: healthy candidate adds while another response is held and another fails", flush=True)


def capacity_isolation():
    a, b, c = torrent(10, 70*MIB), torrent(11, 70*MIB), torrent(12, 70*MIB)
    with App() as app, Fixture(a, free=100*MIB) as one, Fixture(b, free=100*MIB) as two, Fixture(c, free=100*MIB) as other:
        first, pool = downloader(app, one)
        second, _ = downloader(app, two, pool)
        third, _ = downloader(app, other)
        for fixture, instance, item in [(one, first, a), (two, second, b), (other, third, c)]:
            sources(app, fixture, instance)
        one.publish("/rss", a)
        two.publish("/rss", b)
        other.publish("/rss", c)
        confirmed(app, 2)
        time.sleep(3)
        assert len(one.adds) + len(two.adds) == 1 and other.adds == [12]
        assert app.rows("SELECT SUM(bytes) FROM racing_space_commitments WHERE storage_pool_id=?", (pool,)) == [(70*MIB,)]
        assert_clean(one, two, other)
    print("PASS shared capacity: 70+70 contend for 100; one add; independent disk continues", flush=True)


def slow_downloader():
    a, b = torrent(20), torrent(21)
    with App() as app, Fixture(a) as slow, Fixture(b) as fast:
        first, _ = downloader(app, slow)
        second, _ = downloader(app, fast)
        sources(app, slow, first)
        sources(app, fast, second)
        slow.block("sync")
        until(lambda: slow.started["sync"].is_set(), "slow sync not exercised")
        time.sleep(6)  # Existing cache must expire before candidates arrive.
        slow.publish("/rss", a)
        fast.publish("/rss", b)
        confirmed(app)
        assert fast.adds == [21] and slow.adds == []
        assert_clean(slow, fast)
    print("PASS downloader: stale blocked instance does not stop healthy instance", flush=True)


def recovery(boundary):
    item = torrent(30)
    with App() as app, Fixture(item) as f:
        if boundary == "reserved":
            f.show(item)
            f.block("trackers")
        elif boundary == "submitted":
            f.block("add")
        else:
            f.add_mode = "disconnect"
        instance, _ = downloader(app, f)
        sources(app, f, instance)
        f.publish("/rss", item)
        if boundary == "reserved":
            until(lambda: f.started["trackers"].is_set(), "pre-submit tracker check not reached")
        elif boundary == "submitted":
            until(lambda: f.started["add"].is_set(), "submitted request not reached")
        until(lambda: any(i["state"] == boundary for i in intents(app)), f"{boundary} state not observed")
        before = intents(app)[0]
        promise = app.rows("SELECT bytes FROM racing_space_commitments")
        assert promise == [(MIB,)]
        app.stop(crash=True)
        if boundary != "reserved":
            f.show(item)
        for gate in f.gates.values():
            gate.set()
        app.start()
        app.request("/api/auth/login", app.credentials)
        after = confirmed(app)[0]
        assert after["candidateKey"] == before["candidateKey"] and after["instanceId"] == before["instanceId"]
        assert after["reservedAt"] == before["reservedAt"]
        assert app.rows("SELECT bytes FROM racing_space_commitments") == promise
        time.sleep(2)
        assert f.adds == ([] if boundary == "reserved" else [30])
        assert_clean(f)
    print(f"PASS recovery: crash at {boundary}; original target and commitment preserved; no resend", flush=True)


def timeout_isolation():
    a, b = torrent(35), torrent(36)
    with App() as app, Fixture(a) as slow, Fixture(b) as fast:
        slow.block("add")
        first, _ = downloader(app, slow)
        second, _ = downloader(app, fast)
        sources(app, slow, first)
        sources(app, fast, second)
        slow.publish("/rss", a)
        until(lambda: slow.started["add"].is_set(), "held add not reached")
        fast.publish("/rss", b)
        confirmed(app)
        assert fast.adds == [36] and slow.adds == [35]
        assert any(i["instanceId"] == first and i["state"] == "submitted" for i in intents(app)), "healthy instance waited for add timeout"
        until(lambda: any(i["state"] == "unknown" for i in intents(app)), "add did not time out", 30)
        assert app.rows("SELECT COUNT(*) FROM racing_space_commitments") == [(2,)]
        slow.show(a)  # Remote accepted the request despite the missing response.
        confirmed(app, 2)
        assert not slow.gates["add"].is_set()
        assert_clean(slow, fast)
    print("PASS timeout: held add does not block other instance; timeout reconciles without restart or resend", flush=True)


def notification_isolation():
    items = [torrent(n) for n in (40, 41, 42)]
    with App() as app, Fixture(*items) as f:
        f.block("notifications")
        # The existing notifier treats the first nonempty snapshot as its
        # startup baseline. Seed an old task before testing newly added events.
        f.show(torrent(39))
        instance, _ = downloader(app, f)
        until(lambda: f.rid >= 2, "notification baseline did not warm")
        sources(app, f, instance)
        app.request("/api/notifications/targets", dict(name="Synthetic blocked notifications", url=f"generic://127.0.0.1:{f.server.server_port}/hook?template=json&disabletls=yes", enabled=True, eventTypes=["torrent_added"]), expected=201)
        f.publish("/rss", *items[:2])
        confirmed(app, 2)
        until(lambda: f.notifications >= 2, "both notification workers did not block", 40)
        f.publish("/rss", *items)
        confirmed(app, 3)
        assert not f.gates["notifications"].is_set() and 42 in f.adds
        assert_clean(f)
    print("PASS notifications: both workers blocked; next candidate still adds and confirms", flush=True)


def timestamp(value):
    # Python 3.9 accepts microseconds, while Go RFC3339Nano trims zeros.
    whole, dot, fraction = value.removesuffix("Z").partition(".")
    normalized = whole + ("." + (fraction + "000000")[:6] if dot else "")
    return datetime.fromisoformat(normalized + "+00:00").timestamp()


def performance():
    items = [torrent(n) for n in range(100, 112)]
    samples = {key: [] for key in LIMITS}
    with App() as app, Fixture(*items) as f:
        instance, _ = downloader(app, f)
        sources(app, f, instance, interval=10)
        # Warm shared sync first; startup is measured elsewhere.
        until(lambda: f.rid >= 2, "downloader did not warm")
        f.publish("/rss", *items)
        rows = confirmed(app, len(items))
        until(lambda: all("transferredAt" in i for i in intents(app)), "transfer stage missing")
        by_hash = {i["plan"]["hashV1"]: i for i in rows}
        for item in items:
            row = by_hash[item["hash"]]
            seen = timestamp(row["plan"]["firstSeenAt"])
            metadata = f.metadata_at[item["id"]]
            submitted, done = timestamp(row["submittedAt"]), timestamp(row["confirmedAt"])
            samples["discovery"].append(seen - f.published_at[item["id"]])
            samples["metadata"].append(metadata - seen)
            samples["internal"].append(submitted - metadata)
            samples["confirmation"].append(done - submitted)
            samples["total"].append(done - f.published_at[item["id"]])
        assert_clean(f)
        assert len(f.adds) == len(items)
        for key, values in samples.items():
            ordered = sorted(values)
            p50, p95 = ordered[math.ceil(len(ordered)*0.50)-1], ordered[math.ceil(len(ordered)*0.95)-1]
            assert min(values) >= 0 and p95 <= LIMITS[key], (key, p95, LIMITS[key])
            print(f"METRIC {key}: n={len(values)} p50={p50:.3f}s p95={p95:.3f}s limit={LIMITS[key]}s", flush=True)
    print("PASS performance: 12 synthetic candidates; failures=0 unresolved=0 duplicate_adds=0", flush=True)


def main():
    print("C09 preregistered P95 ceilings (seconds): " + json.dumps(LIMITS), flush=True)
    dual_source("/rss")
    dual_source("/torrents.php")
    metadata_isolation()
    capacity_isolation()
    slow_downloader()
    for boundary in ("reserved", "submitted", "unknown"):
        recovery(boundary)
    timeout_isolation()
    notification_isolation()
    performance()


if __name__ == "__main__":
    main()
