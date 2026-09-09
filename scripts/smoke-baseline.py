#!/usr/bin/env python3
"""用临时空数据库验证本地构建的启动、认证、静态资源和重启。"""

import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parents[1]


def main():
    binary = ROOT / ("qui.exe" if os.name == "nt" else "qui")
    if not binary.is_file():
        raise SystemExit("请先运行 make build。")
    with tempfile.TemporaryDirectory(prefix="qui-c00-") as directory:
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        config = Path(directory) / "config.toml"
        config.write_text(
            f'host = "127.0.0.1"\nport = {port}\n'
            'checkForUpdates = false\ntrackerIconsFetchEnabled = false\n'
            'logLevel = "WARN"\n', encoding="utf-8"
        )
        config.chmod(0o600)
        # 不继承可能指向现有实例、数据库或关闭认证的 QUI 环境变量。
        env = {key: value for key, value in os.environ.items() if not key.startswith("QUI__")}
        credentials = {"username": "c00-smoke", "password": secrets.token_urlsafe(24)}
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

        def request(path, expected=200, payload=None, authenticated=True):
            req = urllib.request.Request(
                base + path,
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
                    raise RuntimeError(f"{path}: expected {expected}, got {response.code}")
                return body

        def start(log):
            process = subprocess.Popen(
                [str(binary), "serve", "--config-dir", directory, "--data-dir", directory],
                cwd=directory, env=env, stdout=log, stderr=subprocess.STDOUT,
            )
            try:
                deadline = time.monotonic() + 30
                while time.monotonic() < deadline:
                    if process.poll() is not None:
                        raise RuntimeError("应用启动时退出，请检查启动输出。")
                    try:
                        request("/healthz/readiness")
                        return process
                    except (urllib.error.URLError, TimeoutError, RuntimeError):
                        time.sleep(0.1)
                raise RuntimeError("应用未在 30 秒内就绪。")
            except BaseException:
                stop(process)
                raise

        def stop(process):
            if process.poll() is None:
                process.send_signal(signal.SIGTERM)
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                    raise RuntimeError("应用未能正常停止。")

        with tempfile.TemporaryFile(mode="w+") as log:
            try:
                process = start(log)
                try:
                    assert b'<div id="root"' in request("/")
                    assert json.loads(request("/api/auth/check-setup"))["setupRequired"] is True
                    request("/api/auth/setup", expected=201, payload=credentials)
                    request("/api/auth/logout", payload={})
                    request("/api/instances", expected=403, authenticated=False)
                    request("/api/auth/login", payload=credentials)
                    assert json.loads(request("/api/auth/me"))["username"] == credentials["username"]
                    assert json.loads(request("/api/instances")) == []
                finally:
                    stop(process)
                process = start(log)
                try:
                    assert json.loads(request("/api/auth/check-setup"))["setupRequired"] is False
                    request("/api/auth/login", payload=credentials)
                    assert json.loads(request("/api/instances")) == []
                finally:
                    stop(process)
            except BaseException:
                log.seek(0)
                print(log.read())
                raise
        print("PASS：空库启动、前端入口、账号初始化、退出、未登录拒绝、登录、空实例列表、重启持久化与正常停止。")


if __name__ == "__main__":
    main()
