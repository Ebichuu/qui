"""Shared isolated qui process for synthetic reception checks (standard library only)."""
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def until(check, label, seconds=35):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = check()
        if value:
            return value
        time.sleep(0.1)
    raise RuntimeError(label)


class App:
    def __init__(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="qui-racing-")
        self.directory = Path(self.temporary.name)
        self.log = tempfile.TemporaryFile(mode="w+")
        self.process = None
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        self.base = f"http://127.0.0.1:{port}"
        config = self.directory / "config.toml"
        config.write_text(f'host="127.0.0.1"\nport={port}\ncheckForUpdates=false\nsessionSecret="{secrets.token_urlsafe(32)}"\ntrackerIconsFetchEnabled=false\nlogLevel="WARN"\n', encoding="utf-8")
        config.chmod(0o600)
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.credentials = {"username": "racing-smoke", "password": secrets.token_urlsafe(24)}

    def request(self, path, payload=None, method=None, expected=200):
        req = urllib.request.Request(self.base + path, data=None if payload is None else json.dumps(payload).encode(), method=method, headers={"Content-Type": "application/json"})
        try:
            response = self.opener.open(req, timeout=5)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read()
            assert response.status == expected, f"{path}: {response.status} != {expected}"
            return json.loads(raw) if raw and "application/json" in response.headers.get("Content-Type", "") else None

    def start(self):
        env = {k: v for k, v in os.environ.items() if not k.startswith("QUI__")}
        binary = ROOT / ("qui.exe" if os.name == "nt" else "qui")
        self.process = subprocess.Popen([str(binary), "serve", "--config-dir", str(self.directory), "--data-dir", str(self.directory)], env=env, cwd=self.directory, stdout=self.log, stderr=subprocess.STDOUT)
        def ready():
            if self.process.poll() is not None:
                raise RuntimeError("合成实例启动失败")
            try:
                self.request("/healthz/readiness")
                return True
            except OSError:
                return False
        until(ready, "合成实例启动超时")

    def stop(self, crash=False):
        if self.process is None:
            return
        if crash:
            self.process.kill()
        else:
            self.process.terminate()
        try:
            self.process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait()
            raise RuntimeError("合成实例未能正常停止")
        finally:
            self.process = None

    def restart(self, crash=False):
        self.stop(crash)
        self.start()
        self.request("/api/auth/login", self.credentials)

    def rows(self, query, params=()):
        # Read-only inspection of this private temporary database; never a user DB.
        with sqlite3.connect(f"file:{self.directory / 'qui.db'}?mode=ro", uri=True) as db:
            return db.execute(query, params).fetchall()

    def __enter__(self):
        try:
            self.start()
            self.request("/api/auth/setup", self.credentials, expected=201)
            self.request("/api/auth/login", self.credentials)
            return self
        except BaseException:
            self.__exit__(True, None, None)
            raise

    def __exit__(self, failed, *_args):
        try:
            self.stop()
        finally:
            if failed:
                self.log.seek(0)
                print(self.log.read()[-12000:])
            self.log.close()
            self.temporary.cleanup()
