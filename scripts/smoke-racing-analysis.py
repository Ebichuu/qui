#!/usr/bin/env python3
"""C14 isolated Peer history: incremental sync, gaps, restart and generation."""
from pathlib import Path
from datetime import datetime, timedelta, timezone
import runpy
import sqlite3
import subprocess
import time
import urllib.parse

from racing_smoke import App, until

fixture = runpy.run_path(str(Path(__file__).with_name("smoke-racing-integration.py")))


class Handler(fixture["Handler"]):
    def do_GET(self):
        mock = self.server.fixture
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path.endswith("/sync/torrentPeers"):
            with mock.lock:
                mock.peer_calls += 1
                mode = mock.peer_mode
            if mode == "failed":
                return self.respond("synthetic unavailable", 503)
            if mode == "slow":
                time.sleep(3)
            peers = {} if mode == "empty" else {"[2001:db8::1]:6881": dict(ip="2001:db8::1", port=6881, progress=1, downloaded=12, uploaded=3)}
            return self.respond(dict(rid=mock.peer_calls, full_update=True, peers=peers))
        return super().do_GET()


def main():
    first, second = fixture["torrent"](910), fixture["torrent"](911)
    with App() as app, fixture["Fixture"](first, second) as mock:
        mock.server.RequestHandlerClass = Handler
        mock.peer_calls, mock.peer_mode = 0, "visible"
        original_show = mock.show
        def show(item):
            original_show(item)
            with mock.lock:
                mock.tasks[item["hash"]]["added_on"] = 100
        mock.show = show
        instance, _ = fixture["downloader"](app, mock)
        fixture["sources"](app, mock, instance)
        mock.publish("/rss", first)
        fixture["confirmed"](app)
        intent = next(i for i in fixture["intents"](app) if i["state"] == "confirmed")
        key = intent["candidateKey"]
        path = "/api/racing/analysis-history?candidateKey=" + urllib.parse.quote(key)
        settings = f"/api/racing/analysis-settings/{instance}"
        assert not app.request("/api/racing/analysis-settings")
        history = app.request(path)
        assert history["successfulSamples"] == 0 and history["missingSamples"] == history["expectedSamples"]
        assert mock.peer_calls == 0
        app.request(settings, dict(instanceId=instance, enabled=True), method="PUT", expected=204)
        until(lambda: app.request(path)["successfulSamples"] >= 1, "no peer history")
        history = app.request(path)
        endpoint = "[2001:db8::1]:6881"
        assert history["hash"] == first["hash"] and history["addedOn"] == 100
        assert history["peers"][endpoint]["firstComplete"]
        assert history["asnState"] == "unavailable" and history["rankingState"] == "unsupported"
        # Load an offline synthetic database on restart, without any IP query service.
        database = app.directory / "synthetic.mmdb"
        with database.open("wb") as output:
            subprocess.run(["go", "run", str(Path(__file__).with_name("racing-asn-fixture") / "main.go")], stdout=output, check=True)
        with (app.directory / "config.toml").open("a") as config:
            config.write('\nracingASNDatabasePath="synthetic.mmdb"\n')
        app.restart()
        until(lambda: app.request(path)["asnState"] == "available", "offline ASN not recorded")
        asn = app.request(path)["peers"][endpoint]["asn"]
        assert asn["state"] == "found" and asn["number"] == 64512
        assert asn["organization"] == "Synthetic ASN" and len(asn["databaseSHA256"]) == 64
        # A broken database must leave existing evidence intact and sampling active.
        database.write_bytes(b"synthetic invalid database")
        app.restart()
        assert app.request(path)["peers"][endpoint]["asn"] == asn
        mock.peer_mode = "failed"
        until(lambda: any(s["state"] == "peers_unavailable" for s in app.request(path)["samples"]), "failure gap not recorded")
        assert "absentAt" not in app.request(path)["peers"][endpoint]
        app.restart(crash=True)
        mock.peer_mode = "empty"
        until(lambda: "absentAt" in app.request(path)["peers"][endpoint], "absence not recorded after restart")
        assert any(s["state"] == "peers_unavailable" for s in app.request(path)["samples"])
        mock.peer_mode = "slow"
        mock.publish("/rss", first, second)
        fixture["confirmed"](app, 2)
        assert mock.adds == [first["id"], second["id"]], "analysis blocked or duplicated reception"
        with mock.lock:
            mock.tasks[first["hash"]]["added_on"] = int(time.time()) + 1
        until(lambda: any(s["state"] == "generation_changed" for s in app.request(path)["samples"]), "replacement generation was merged")
        app.request(settings, dict(instanceId=instance, enabled=False), method="PUT", expected=204)
        time.sleep(4)
        calls = mock.peer_calls
        time.sleep(6)
        assert calls == mock.peer_calls, "disabled analysis still samples"
        history = app.request(path)
        assert history["missingSamples"] > 0 and history["peers"][endpoint]["samples"] >= 1
        assert not mock.errors
        # Age only this disposable stopped instance, then verify the real API.
        app.stop()
        expired = (datetime.now(timezone.utc) - timedelta(days=31)).isoformat().replace("+00:00", "Z")
        with sqlite3.connect(app.directory / "qui.db") as db:
            db.execute("UPDATE racing_add_intents SET confirmed_at=? WHERE candidate_key=?", (expired, key))
            db.execute("UPDATE racing_analysis_history SET updated_at=? WHERE candidate_key=?", (expired, key))
        app.start()
        app.request("/api/auth/login", app.credentials)
        app.request(path, expected=404)
        assert mock.adds == [first["id"], second["id"]], "history expiry replayed reception"
        print("PASS Peer history: explicit read-only switch, offline ASN and unavailable database recovery, exact hash, IP/port, gaps, restart, disappearance, generation change, independent reception, expired history unavailable")


if __name__ == "__main__":
    main()
