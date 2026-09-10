#!/usr/bin/env python3
"""在独立空库验证 Q1 配置、认证、秘密字段、重启及只读边界。"""

from datetime import datetime, timedelta, timezone
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
import urllib.parse

ROOT = Path(__file__).resolve().parents[1]


class SourceTrap(http.server.BaseHTTPRequestHandler):
    requests = 0

    def log_message(self, *_args):
        pass

    def do_GET(self):
        type(self).requests += 1
        self.send_response(503)
        self.end_headers()

    do_POST = do_GET


class DiscoveryFixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        self.server.calls += 1
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path == "/rss":
            self.server.rss_started.set()
            self.server.release.wait(30)
            body = self.rss(42, "/details.php?id=42")
        elif parsed.path == "/mteam":
            body = self.rss(73, "/detail/73")
        elif parsed.path == "/torrents.php":
            if urllib.parse.parse_qs(parsed.query).get("page") == ["1"]:
                self.server.later_page.set()
                self.server.release.wait(30)
                self.send_response(503)
                self.end_headers()
                return
            stamp = self.server.published.astimezone(timezone(timedelta(hours=8))).strftime("%Y-%m-%d %H:%M:%S")
            body = f'<table class="torrents"><tr><td><a href="details.php?id=42" title="Example Aurora Full Synthetic Title-CHD"><b>Example Aurora...</b></a><span>官方</span><a href="download.php?id=42&amp;key=synthetic-private">Download</a></td><td>1 GiB</td><td class="rowfollow nowrap"><span title="{stamp}">now</span></td></tr></table>'
        else:
            self.send_response(404)
            self.end_headers()
            return
        data = body.encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        try:
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def rss(self, torrent_id, details):
        current = self.server.published.strftime("%a, %d %b %Y %H:%M:%S +0000")
        return f'<rss><channel><item><title>Example Aurora Full Synthetic Title-CHD</title><link>{details}</link><pubDate>{current}</pubDate><enclosure url="/download?id={torrent_id}&amp;key=synthetic-private" length="1073741824"/><attr name="downloadvolumefactor" value="0"/></item><item><title>Example Old Entry</title><link>/details.php?id=1</link><pubDate>Thu, 01 Jan 2015 00:00:00 +0000</pubDate><enclosure url="/download?id=1" length="1024"/></item></channel></rss>'


class DownloaderMock(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def respond(self, value):
        data = (value if isinstance(value, str) else json.dumps(value)).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        try:
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", 0)))
        if self.path.endswith("/auth/login"):
            return self.respond("Ok.")
        self.server.actions.append(self.path)
        self.respond("")

    def do_GET(self):
        path = urllib.parse.urlparse(self.path).path
        if path.endswith("/app/version"):
            return self.respond("v5.1.2")
        if path.endswith("/app/webapiVersion"):
            return self.respond("2.11.4")
        if path.endswith("/app/buildInfo"):
            return self.respond({"qt": "6.8", "libtorrent": "2.0.11", "bitness": 64})
        if path.endswith("/app/preferences"):
            return self.respond({"save_path": "/synthetic/disk-a", "temp_path": "/synthetic/disk-b", "temp_path_enabled": True})
        if path.endswith("/sync/maindata"):
            self.server.samples += 1
            if self.server.stalled:
                time.sleep(15)
            return self.respond({"rid": self.server.samples, "full_update": True, "torrents": {},
                "categories": {"other": {"name": "other", "savePath": "/synthetic/disk-b"}}, "tags": [],
                "server_state": {"connection_status": "connected", "free_space_on_disk": 1000000,
                    "up_info_speed": 1234, "dl_info_speed": 0, "alltime_ul": self.server.samples * 1000}})
        self.respond([])


def main():
    binary = ROOT / ("qui.exe" if os.name == "nt" else "qui")
    env = {key: value for key, value in os.environ.items() if not key.startswith("QUI__")}
    with tempfile.TemporaryDirectory(prefix="qui-c03-") as directory:
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        downloaders = []
        for _ in range(2):
            mock = http.server.ThreadingHTTPServer(("127.0.0.1", 0), DownloaderMock)
            mock.samples, mock.actions, mock.stalled = 0, [], False
            threading.Thread(target=mock.serve_forever, daemon=True).start()
            downloaders.append(mock)
        source = http.server.ThreadingHTTPServer(("127.0.0.1", 0), SourceTrap)
        threading.Thread(target=source.serve_forever, daemon=True).start()
        origin = f"http://127.0.0.1:{source.server_port}"
        discovery = http.server.ThreadingHTTPServer(("127.0.0.1", 0), DiscoveryFixture)
        discovery.calls = 0
        discovery.published = datetime.now(timezone.utc)
        discovery.release, discovery.rss_started, discovery.later_page = threading.Event(), threading.Event(), threading.Event()
        threading.Thread(target=discovery.serve_forever, daemon=True).start()
        config = Path(directory) / "config.toml"
        config.write_text(
            f'host="127.0.0.1"\nport={port}\ncheckForUpdates=false\n'
            f'sessionSecret="{secrets.token_urlsafe(32)}"\n'
            'trackerIconsFetchEnabled=false\nlogLevel="WARN"\n', encoding="utf-8"
        )
        config.chmod(0o600)
        credentials = {"username": "c03-smoke", "password": secrets.token_urlsafe(24)}
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

        def request(path, payload=None, method=None, expected=200, authenticated=True):
            req = urllib.request.Request(
                base + path, method=method,
                data=None if payload is None else json.dumps(payload).encode(),
                headers={"Content-Type": "application/json"},
            )
            client = opener if authenticated else urllib.request.build_opener()
            try:
                response = client.open(req, timeout=5)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                body = response.read()
                if response.code != expected:
                    raise RuntimeError(f"{method or 'GET'} {path}: expected {expected}, got {response.code}")
                if not body:
                    return None
                if "application/json" in response.headers.get("Content-Type", ""):
                    return json.loads(body)
                return body.decode()

        def start(log):
            process = subprocess.Popen(
                [str(binary), "serve", "--config-dir", directory, "--data-dir", directory],
                env=env, cwd=directory, stdout=log, stderr=subprocess.STDOUT,
            )
            try:
                deadline = time.monotonic() + 30
                while time.monotonic() < deadline:
                    if process.poll() is not None:
                        raise RuntimeError("Q1 程序启动失败")
                    try:
                        request("/healthz/readiness")
                        return process
                    except (OSError, RuntimeError):
                        time.sleep(0.1)
                raise RuntimeError("Q1 程序启动超时")
            except BaseException:
                process.terminate()
                process.wait(timeout=15)
                raise

        def stop(process):
            process.terminate()
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
                raise RuntimeError("Q1 程序未能正常停止")

        with tempfile.TemporaryFile(mode="w+") as log:
            process = None
            try:
                process = start(log)
                request("/api/racing/configuration", expected=428, authenticated=False)
                request("/api/auth/setup", credentials, expected=201)
                request("/api/racing/configuration", expected=403, authenticated=False)
                request("/api/auth/login", credentials)
                assert request("/api/racing/configuration") == dict(sites=[], sources=[], groups=[], rules=[], storagePools=[], pathMappings=[])
                site = request("/api/racing/sites", dict(name="Synthetic site", baseUrl=origin,
                    enabled=True, requestIntervalSeconds=5, credential="synthetic-cookie-secret"), expected=201)["id"]
                feed = request("/api/racing/sources", dict(name="Synthetic feed", siteId=site,
                    kind="rss", enabled=False, intervalSeconds=1,
                    url=origin + "/private?passkey=synthetic-source-secret"), expected=201)["id"]
                group = request("/api/racing/groups", dict(name="Synthetic group", enabled=True,
                    instanceIds=[]), expected=201)["id"]
                rule = request("/api/racing/rules", dict(name="Synthetic rule", enabled=True,
                    sourceIds=[feed], acceptKinds=["official", "free"], receiveWindowSeconds=900,
                    targetGroupId=group, allowOfficialReclaim=True), expected=201)["id"]
                request(f"/api/racing/sites/{site}", dict(name="Renamed site", baseUrl=origin,
                    enabled=True, requestIntervalSeconds=5), method="PUT")
                request(f"/api/racing/groups/{group}", method="DELETE", expected=409)
                instance_ids = []
                for index, mock in enumerate(downloaders):
                    instance = request("/api/instances", dict(name=f"Synthetic downloader {index + 1}",
                        host=f"http://127.0.0.1:{mock.server_port}", username="synthetic", password="synthetic"), expected=201)
                    instance_ids.append(instance["id"])
                request(f"/api/racing/groups/{group}", dict(name="Synthetic group", enabled=True,
                    instanceIds=instance_ids + instance_ids), method="PUT")
                pool = request("/api/racing/storage-pools", dict(name="Shared disk A"), expected=201)["id"]
                other_pool = request("/api/racing/storage-pools", dict(name="Other disk"), expected=201)["id"]
                mappings = []
                for instance_id in instance_ids:
                    mappings.append(request("/api/racing/path-mappings", dict(instanceId=instance_id,
                        storagePoolId=pool, path="/synthetic/disk-a"), expected=201)["id"])
                mappings.append(request("/api/racing/path-mappings", dict(instanceId=instance_ids[0],
                    storagePoolId=other_pool, path="/synthetic/disk-b"), expected=201)["id"])
                request(f"/api/racing/storage-pools/{pool}", method="DELETE", expected=409)
                deadline = time.monotonic() + 30
                while True:
                    observation = request("/api/racing/observations")
                    capacity = {item["storagePoolId"]: item.get("freeBytes") for item in observation["storagePools"]}
                    if len(observation["instances"]) == 2 and all(item["fresh"] for item in observation["instances"]) and capacity.get(pool) == 1000000:
                        break
                    assert time.monotonic() < deadline, "background state did not become ready"
                    time.sleep(0.2)
                assert capacity[other_pool] is None, "default directory space must not count for another disk"
                start_count = downloaders[1].samples
                downloaders[0].stalled = True
                time.sleep(7)
                observation = request("/api/racing/observations")
                state = {item["instanceId"]: item for item in observation["instances"]}
                assert not state[instance_ids[0]]["fresh"]
                assert state[instance_ids[1]]["fresh"] and downloaders[1].samples >= start_count + 2
                downloaders[0].stalled = False
                before = request("/api/racing/configuration")
                assert before["sites"][0]["id"] == site and before["sites"][0]["hasCredential"]
                assert before["sources"][0]["urlOrigin"] == origin
                encoded = json.dumps(before)
                assert "synthetic-cookie-secret" not in encoded and "synthetic-source-secret" not in encoded
                assert "/private" not in encoded
                deadline = time.monotonic() + 5
                while True:
                    status = request("/api/racing/status")
                    if status["rules"] == 1 and status["configurationReady"]:
                        break
                    assert time.monotonic() < deadline
                    time.sleep(0.05)
                assert status["mode"] == "observe_only" and status["running"]
                stop(process)
                process = start(log)
                request("/api/auth/login", credentials)
                assert request("/api/racing/configuration") == before
                status = request("/api/racing/status")
                assert status["running"] and status["configurationReady"] and status["rules"] == 1
                assert SourceTrap.requests == 0, "disabled source must not be fetched"
                assert all(not mock.actions for mock in downloaders), "Q1 must not add, delete or modify downloader settings"
                discovery.published = datetime.now(timezone.utc)
                discovery_origin = f"http://127.0.0.1:{discovery.server_port}"
                discovery_site = request("/api/racing/sites", dict(name="Synthetic discovery site", baseUrl=discovery_origin,
                    enabled=True, requestIntervalSeconds=1, credential="sid=synthetic-private"), expected=201)["id"]
                source_inputs = [
                    dict(name="Slow RSS", kind="rss", adapter="chd", url=discovery_origin + "/rss"),
                    dict(name="Fast web", kind="web", adapter="chd", url=discovery_origin + "/torrents.php", pageCount=2),
                    dict(name="M-Team fixture", kind="rss", adapter="mteam", url=discovery_origin + "/mteam"),
                ]
                discovery_ids = []
                for index, payload in enumerate(source_inputs):
                    payload.update(siteId=discovery_site, enabled=True, intervalSeconds=1, initialLookbackSeconds=60)
                    discovery_ids.append(request("/api/racing/sources", payload, expected=201)["id"])
                    if index == 0:
                        assert discovery.rss_started.wait(5), "RSS source did not start"
                deadline = time.monotonic() + 8
                while True:
                    found = request("/api/racing/discoveries")
                    web_items = [item for item in found["items"] if item["sourceId"] == discovery_ids[1]]
                    if web_items:
                        break
                    assert time.monotonic() < deadline, "fast web waited for slow RSS"
                    time.sleep(0.05)
                assert not discovery.release.is_set()
                assert web_items[0]["item"]["title"] == "Example Aurora Full Synthetic Title-CHD"
                assert web_items[0]["eligible"] and web_items[0]["item"]["official"]["value"] == "true"
                assert discovery.later_page.wait(5), "second page did not start"
                assert any(item["sourceId"] == discovery_ids[1] for item in request("/api/racing/discoveries")["items"]), "first page waited for later page"
                first = web_items[0]
                discovery.release.set()
                deadline = time.monotonic() + 8
                while True:
                    found = request("/api/racing/discoveries")
                    sources_seen = {item["sourceId"] for item in found["items"]}
                    if all(source_id in sources_seen for source_id in discovery_ids):
                        break
                    assert time.monotonic() < deadline, "source fixture did not produce observations"
                    time.sleep(0.05)
                assert "synthetic-private" not in json.dumps(found)
                assert len(found["capabilities"]) == 3
                assert all(not item["eligible"] for item in found["items"] if item["item"]["torrentId"] == "1")
                assert any(item["item"]["torrentId"] == "73" and item["item"]["free"]["value"] == "true" for item in found["items"])
                stop(process)
                process = start(log)
                request("/api/auth/login", credentials)
                deadline = time.monotonic() + 8
                while True:
                    after = request("/api/racing/discoveries")
                    current = next(item for item in after["items"] if item["id"] == first["id"])
                    if current["lastSeenAt"] != first["lastSeenAt"]:
                        break
                    assert time.monotonic() < deadline
                    time.sleep(0.1)
                assert current["firstSeenAt"] == first["firstSeenAt"]
                assert all(not item["eligible"] for item in after["items"] if item["item"]["torrentId"] == "1")
                for source_id in discovery_ids:
                    request(f"/api/racing/sources/{source_id}", method="DELETE", expected=204)
                request(f"/api/racing/sites/{discovery_site}", method="DELETE", expected=204)
                assert all(not mock.actions for mock in downloaders), "discovery must not mutate downloaders"
                for mapping in mappings:
                    request(f"/api/racing/path-mappings/{mapping}", method="DELETE", expected=204)
                for storage_pool in [pool, other_pool]:
                    request(f"/api/racing/storage-pools/{storage_pool}", method="DELETE", expected=204)
                request(f"/api/racing/rules/{rule}", method="DELETE", expected=204)
                request(f"/api/racing/groups/{group}", method="DELETE", expected=204)
                request(f"/api/racing/sources/{feed}", method="DELETE", expected=204)
                request(f"/api/racing/sites/{site}", method="DELETE", expected=204)
                print("PASS：Q1 认证、配置保存/改名、敏感字段隐藏、引用保护、进程重启恢复、后台独立同步、慢实例隔离、共盘去重、异盘空间未知、停用来源不抓取；RSS/网页独立逐项发现、后页不阻塞、两站适配夹具、旧条目基线、重启去重、不执行下载器动作。")
            except BaseException:
                log.seek(0)
                print(log.read()[-12000:])
                raise
            finally:
                if process is not None and process.poll() is None:
                    stop(process)
                discovery.release.set()
                discovery.shutdown()
                discovery.server_close()
                source.shutdown()
                source.server_close()
                for mock in downloaders:
                    mock.shutdown()
                    mock.server_close()


if __name__ == "__main__":
    main()
