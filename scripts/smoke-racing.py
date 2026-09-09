#!/usr/bin/env python3
"""在独立空库验证 Q1 配置、认证、秘密字段、重启及只读边界。"""

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


def main():
    binary = ROOT / ("qui.exe" if os.name == "nt" else "qui")
    env = {key: value for key, value in os.environ.items() if not key.startswith("QUI__")}
    with tempfile.TemporaryDirectory(prefix="qui-c03-") as directory:
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        source = http.server.ThreadingHTTPServer(("127.0.0.1", 0), SourceTrap)
        threading.Thread(target=source.serve_forever, daemon=True).start()
        origin = f"http://127.0.0.1:{source.server_port}"
        config = Path(directory) / "config.toml"
        config.write_text(
            f'host="127.0.0.1"\nport={port}\ncheckForUpdates=false\n'
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
                assert request("/api/racing/configuration") == dict(sites=[], sources=[], groups=[], rules=[])
                site = request("/api/racing/sites", dict(name="Synthetic site", baseUrl=origin,
                    enabled=True, requestIntervalSeconds=5, credential="synthetic-cookie-secret"), expected=201)["id"]
                feed = request("/api/racing/sources", dict(name="Synthetic feed", siteId=site,
                    kind="rss", enabled=True, intervalSeconds=1,
                    url=origin + "/private?passkey=synthetic-source-secret"), expected=201)["id"]
                group = request("/api/racing/groups", dict(name="Synthetic group", enabled=True,
                    instanceIds=[]), expected=201)["id"]
                rule = request("/api/racing/rules", dict(name="Synthetic rule", enabled=True,
                    sourceIds=[feed], acceptKinds=["official", "free"], receiveWindowSeconds=900,
                    targetGroupId=group, allowOfficialReclaim=True), expected=201)["id"]
                request(f"/api/racing/sites/{site}", dict(name="Renamed site", baseUrl=origin,
                    enabled=True, requestIntervalSeconds=5), method="PUT")
                request(f"/api/racing/groups/{group}", method="DELETE", expected=409)
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
                assert SourceTrap.requests == 0, "Q1 must not fetch sources or execute actions"
                request(f"/api/racing/rules/{rule}", method="DELETE", expected=204)
                request(f"/api/racing/groups/{group}", method="DELETE", expected=204)
                request(f"/api/racing/sources/{feed}", method="DELETE", expected=204)
                request(f"/api/racing/sites/{site}", method="DELETE", expected=204)
                print("PASS：Q1 认证、配置保存/改名、敏感字段隐藏、引用保护、进程重启恢复、只观察且不请求来源。")
            except BaseException:
                log.seek(0)
                print(log.read()[-12000:])
                raise
            finally:
                if process is not None and process.poll() is None:
                    stop(process)
                source.shutdown()
                source.server_close()


if __name__ == "__main__":
    main()
