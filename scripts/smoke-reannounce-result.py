#!/usr/bin/env python3
"""Run a real qui process against synthetic multi-tracker responses."""
from pathlib import Path
import runpy
import time
import urllib.parse

from racing_smoke import App, until

fixture = runpy.run_path(str(Path(__file__).with_name("smoke-racing-integration.py")))


class Handler(fixture["Handler"]):
    def do_GET(self):
        mock = self.server.fixture
        path = urllib.parse.urlparse(self.path).path
        if path.endswith("/torrents/info"):
            with mock.lock:
                return self.respond(list(mock.tasks.values()))
        if path.endswith("/torrents/trackers"):
            with mock.lock:
                rows = [{"url":"https://a.example.invalid/announce", "status":4}, {"url":"https://b.example.invalid/announce", "status":4}]
                if mock.posts and mock.mode == "partial":
                    rows[0]["status"] = 2
                return self.respond(rows)
        return super().do_GET()

    def do_POST(self):
        mock = self.server.fixture
        if self.path.endswith("/torrents/reannounce"):
            self.rfile.read(int(self.headers.get("Content-Length", 0)))
            with mock.lock:
                mock.posts += 1
            return self.respond("")
        return super().do_POST()


def main():
    for mode, outcome in [("failed", "failed"), ("partial", "skipped")]:
        item = fixture["torrent"](730)
        mock = fixture["Fixture"](item)
        mock.server.RequestHandlerClass = Handler
        mock.mode, mock.posts = mode, 0
        mock.show(item)
        mock.tasks[item["hash"]].update(added_on=int(time.time())-30, state="stalledUP", dlspeed=0)
        with App() as app, mock:
            instance = app.request("/api/instances", dict(name="Synthetic tracker verification", host=mock.origin, username="synthetic", password="synthetic", reannounceSettings=dict(enabled=True, monitorAll=True, initialWaitSeconds=1, reannounceIntervalSeconds=1, maxAgeSeconds=600, maxRetries=1)), expected=201)["id"]
            def finished():
                rows = app.request(f"/api/instances/{instance}/reannounce/activity")
                return [row for row in rows if row["outcome"] in ("failed", "skipped", "succeeded")]
            rows = until(finished, "reannounce result was not verified", 35)
            assert rows[0]["outcome"] == outcome, rows
            assert mock.posts == 1, (mock.posts, rows)
            assert not mock.errors, mock.errors
            history_path = f"/api/instances/{instance}/reannounce/observations"
            history = app.request(history_path)
            assert len(history) == 1 and history[0]["hash"] == item["hash"], history
            assert len(history[0]["trackers"]) == 2, history
            states = {row["host"]: row["state"] for row in history[0]["trackers"]}
            assert states["b.example.invalid"] == "error_unknown", states
            assert states["a.example.invalid"] == ("reported_working" if mode == "partial" else "error_unknown"), states
            app.request(f"/api/instances/{instance}", dict(name="Synthetic tracker verification", host=mock.origin, username="synthetic", reannounceSettings=dict(enabled=False)), method="PUT")
            app.restart(crash=True)
            assert app.request(history_path) == history, "historical tracker observations did not survive restart"
    print("PASS C12 retry and observations: failed and partial results remain distinct; each scenario sends one request; per-tracker history survives restart")


if __name__ == "__main__":
    main()
