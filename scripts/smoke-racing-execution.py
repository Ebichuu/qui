#!/usr/bin/env python3
"""验证 C08 的默认停用、真实添加链路、分阶段确认及重启防重复；只访问本机合成服务。"""
from datetime import datetime, timezone
import hashlib
import http.server
import json
import threading
import time
import urllib.parse

from racing_smoke import App, until

ANNOUNCE = "https://tracker.invalid/announce?key=synthetic"
INFO = b"d6:lengthi1024e4:name14:Example Aurora12:piece lengthi16384e6:pieces20:abcdefghijklmnopqrste"
META = f"d8:announce{len(ANNOUNCE)}:{ANNOUNCE}4:info".encode() + INFO + b"e"
HASH = hashlib.sha1(INFO).hexdigest()

class Mock(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def respond(self, data):
        if not isinstance(data, bytes):
            data = (data if isinstance(data, str) else json.dumps(data)).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        try:
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_GET(self):
        path = urllib.parse.urlparse(self.path).path
        if path == "/rss":
            stamp = self.server.published.strftime("%a, %d %b %Y %H:%M:%S +0000")
            return self.respond(f'<rss><channel><item><title>Example Aurora-CHD</title><link>/details.php?id=42</link><pubDate>{stamp}</pubDate><enclosure url="/download?id=42" length="1024"/><attr name="downloadvolumefactor" value="0"/></item></channel></rss>')
        if path == "/download":
            self.server.metadata_calls += 1
            return self.respond(META)
        if path.endswith("/app/version"):
            return self.respond("v5.1.2")
        if path.endswith("/app/webapiVersion"):
            return self.respond("2.11.4")
        if path.endswith("/app/buildInfo"):
            return self.respond({"qt": "6.8", "libtorrent": "2.0.11", "bitness": 64})
        if path.endswith("/app/preferences"):
            return self.respond({"save_path": "/synthetic/disk", "temp_path_enabled": False})
        if path.endswith("/torrents/trackers"):
            return self.respond([{"url": ANNOUNCE, "status": 2}])
        if path.endswith("/sync/maindata"):
            self.server.rid += 1
            torrents = {}
            if self.server.adds:
                torrents[HASH] = {"hash": HASH, "infohash_v1": HASH, "name": "Example Aurora-CHD", "save_path": "/synthetic/disk", "size": 1024, "total_size": 1024, "amount_left": 1024, "completed": 0, "downloaded": 0, "uploaded": 0, "state": "pausedDL"}
                if self.server.running:
                    torrents[HASH].update(state="downloading", downloaded=1, dlspeed=1)
            return self.respond({"rid": self.server.rid, "full_update": True, "torrents": torrents, "categories": {}, "tags": [], "server_state": {"connection_status": "connected", "free_space_on_disk": 1000000, "dl_info_speed": 0, "up_info_speed": 0}})
        return self.respond([])

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        if self.path.endswith("/auth/login"):
            return self.respond("Ok.")
        if self.path.endswith("/torrents/add"):
            assert INFO in body, "添加请求必须携带经过验证的 metainfo"
            self.server.adds += 1
            return self.respond({"success_count": 1, "pending_count": 0, "failure_count": 0, "added_torrent_ids": [HASH]})
        self.server.unexpected.append(self.path)
        return self.respond("Ok.")


def main():
    mock = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Mock)
    mock.published = datetime.now(timezone.utc)
    mock.rid, mock.adds, mock.metadata_calls = 0, 0, 0
    mock.running, mock.unexpected = False, []
    threading.Thread(target=mock.serve_forever, daemon=True).start()
    try:
        with App() as app:
            request = app.request
            origin = f"http://127.0.0.1:{mock.server_port}"
            instance = request("/api/instances", dict(name="Synthetic C08 downloader", host=origin, username="synthetic", password="synthetic"), expected=201)["id"]
            site = request("/api/racing/sites", dict(name="Synthetic C08 site", baseUrl=origin, enabled=True, trackerHosts=["tracker.invalid"], requestIntervalSeconds=1), expected=201)["id"]
            source = request("/api/racing/sources", dict(name="Synthetic C08 RSS", siteId=site, kind="rss", adapter="chd", enabled=True, intervalSeconds=2, initialLookbackSeconds=300, url=origin+"/rss"), expected=201)["id"]
            pool = request("/api/racing/storage-pools", dict(name="Synthetic C08 disk"), expected=201)["id"]
            request("/api/racing/path-mappings", dict(instanceId=instance, storagePoolId=pool, path="/synthetic/disk"), expected=201)
            request("/api/racing/rules", dict(name="Synthetic C08 official", enabled=True, sourceIds=[source], acceptKinds=["official"], receiveWindowSeconds=900, targetInstanceId=instance), expected=201)
            until(lambda: any(c["state"] == "ready" for c in request("/api/racing/candidates")["items"]), "候选未进入可接种状态")
            assert request("/api/racing/reception-policies") == []
            assert mock.adds == 0, "默认停用时发生添加"
            assert mock.metadata_calls == 0, "默认停用时不应为接种额外抓取 metainfo"
            policy = dict(instanceId=instance, enabled=True, maxConcurrentAdds=1, maxActiveDownloads=8, minFreeBytes=10, savePath="/synthetic/disk", category="", autoTMM=False, startPaused=True)
            request(f"/api/racing/reception-policies/{instance}", policy, method="PUT", expected=204)
            def confirmed():
                items = request("/api/racing/add-intents")["items"]
                return items and items[0]["state"] == "confirmed"
            until(confirmed, "添加或 Tracker 核实未完成")
            intent = request("/api/racing/add-intents")["items"][0]
            assert mock.adds == 1 and "acceptedAt" in intent and "runnableAt" not in intent and "transferredAt" not in intent
            assert ANNOUNCE not in json.dumps(intent) and "key=synthetic" not in json.dumps(intent), "记录泄露私密 transport"
            mock.running = True
            until(lambda: "transferredAt" in request("/api/racing/add-intents")["items"][0], "首次传输未记录", seconds=40)
            policy["enabled"] = False
            request(f"/api/racing/reception-policies/{instance}", policy, method="PUT", expected=204)
            app.restart()
            assert request("/api/racing/add-intents")["items"][0]["state"] == "confirmed"
            until(lambda: request("/api/racing/status")["mode"] == "observe_only", "停用未持久化")
            time.sleep(2)
            assert mock.adds == 1 and not mock.unexpected, "重启重复添加或存在非预期写操作"
            print("PASS C08: default disabled; verified metainfo; one add; paused/transfer stages; restart preserves intent; no duplicate or tracker mutation")
    finally:
        mock.shutdown()
        mock.server_close()

if __name__ == "__main__":
    main()
