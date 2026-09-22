#!/usr/bin/env python3
"""Isolated native filesystem + mock qB: official reclaim, restart, and unknown."""
import hashlib
import os
from pathlib import Path
import runpy
import socket
import sys
import tempfile
import time
import urllib.parse

from racing_smoke import App, until

fixture = runpy.run_path(str(Path(__file__).with_name("smoke-racing-integration.py")))
MIB = fixture["MIB"]


class Handler(fixture["Handler"]):
    def do_GET(self):
        mock = self.server.fixture
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path.endswith("/torrents/info"):
            with mock.lock:
                return self.respond(list(mock.tasks.values()))
        if parsed.path.endswith("/app/preferences"):
            return self.respond(dict(save_path=mock.root, temp_path_enabled=False))
        if parsed.path.endswith("/torrents/files"):
            query = urllib.parse.parse_qs(parsed.query)
            if query.get("hash") == [mock.old["hash"]]:
                return self.respond([dict(index=0, name="payload.bin", size=mock.old["size"], progress=1, priority=1, is_seed=True)])
            return self.respond([])
        return super().do_GET()

    def do_POST(self):
        if not self.path.endswith("/torrents/delete"):
            return super().do_POST()
        body = urllib.parse.parse_qs(self.rfile.read(int(self.headers.get("Content-Length", 0))).decode())
        mock = self.server.fixture
        assert body.get("hashes") == [mock.old["hash"]], body
        assert body.get("deleteFiles") == ["true"], body
        with mock.lock:
            mock.deletes += 1
            mock.tasks.pop(mock.old["hash"], None)
            # A qB capacity change cannot bypass the native release receipt.
            mock.free = 50 * MIB
        if mock.delete_mode == "disconnect":
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return
        self.respond("Ok.")


def run(mode, root):
    incoming, old = fixture["torrent"](880, 10*MIB), fixture["torrent"](881, 16*MIB)
    payload = Path(root, "payload.bin")
    with payload.open("wb") as stream:
        stream.write(b"x" * old["size"])
        stream.flush()
        os.fsync(stream.fileno())
    with App() as app, fixture["Fixture"](incoming, old, free=MIB) as mock:
        mock.root, mock.old, mock.deletes, mock.delete_mode = root, old, 0, mode
        mock.server.RequestHandlerClass = Handler
        original_show = mock.show
        def show(item):
            original_show(item)
            with mock.lock:
                mock.tasks[item["hash"]]["save_path"] = root
        mock.show = show
        mock.show(old)
        mock.tasks[old["hash"]].update(save_path=root, added_on=100, completed=old["size"], amount_left=0, state="stalledUP", dlspeed=0, upspeed=1)
        instance = app.request("/api/instances", dict(name="Synthetic official executor", host=mock.origin, username="synthetic", password="synthetic", hasLocalFilesystemAccess=True), expected=201)["id"]
        pool = app.request("/api/racing/storage-pools", dict(name="Synthetic isolated filesystem"), expected=201)["id"]
        app.request("/api/racing/path-mappings", dict(instanceId=instance, storagePoolId=pool, path=root), expected=201)
        reception = dict(instanceId=instance, enabled=True, reclaimEnabled=False, maxConcurrentAdds=1, maxActiveDownloads=10, minFreeBytes=0, savePath=root, category="", autoTMM=False, startPaused=False)
        app.request(f"/api/racing/reception-policies/{instance}", reception, method="PUT", expected=204)
        fixture["sources"](app, mock, instance)
        config = app.request("/api/racing/configuration")
        rule = config["rules"][0]
        rule = {key: rule[key] for key in ("name", "enabled", "sourceIds", "acceptKinds", "receiveWindowSeconds", "targetInstanceId")}
        rule["allowOfficialReclaim"] = True
        app.request(f'/api/racing/rules/{config["rules"][0]["id"]}', rule, method="PUT")
        tracker = dict(trackerKey=hashlib.sha256(fixture["ANNOUNCE"].encode()).hexdigest(), siteId=config["sites"][0]["id"], trackerHost="tracker.invalid", intervalSeconds=1, waitMessageDigest="", waitSeconds=0, deleteProtection="reported_working")
        app.request(f"/api/instances/{instance}/reannounce/tracker-policies", tracker, method="PUT", expected=204)
        path = f"/api/instances/{instance}/automations"
        workflow = app.request(path, dict(name="Synthetic mature official candidate", trackerPattern="*", enabled=True, dryRun=False, conditions={"schemaVersion":"1", "delete":{"usage":"official", "enabled":True,"mode":"deleteWithFiles","conditionMatchDurationSeconds":60,"condition":{"field":"UP_SPEED","operator":"LESS_THAN","value":"10"}}}), expected=201)
        policy = dict(enabled=True, ruleIds=[workflow["id"]], maxDeletes=2, maxReclaimBytes=64*MIB, maxRecentUploadBytes=1000, recentUploadWindowSeconds=60, maxOvershootBytes=32*MIB)
        app.request(f"/api/racing/reclaim-settings/instances/{instance}", policy, method="PUT", expected=204)
        until(lambda: mock.rid >= 2, "snapshot did not warm")
        end = time.monotonic() + 68
        while time.monotonic() < end:
            app.request(path + "/reclaim-candidates")
            time.sleep(2)
        mock.publish("/rss", incoming)
        try:
            until(lambda: app.request("/api/racing/reclaim-plans"), "native evidence did not prepare a plan", 60)
        except RuntimeError:
            print("ASSESSMENT", app.request("/api/racing/reclaim-assessments"), flush=True)
            print("CANDIDATES", app.request(path + "/reclaim-candidates"), flush=True)
            raise
        assert mock.deletes == 0, "reception/settings implicitly granted deletion"
        reception["reclaimEnabled"] = True
        app.request(f"/api/racing/reception-policies/{instance}", reception, method="PUT", expected=204)
        until(lambda: mock.deletes == 1, "authorized step was not sent", 60)
        expected = "accepted" if mode == "accepted" else "unknown"
        until(lambda: app.rows("SELECT state FROM automatic_delete_intents") in ([(expected,)], [("confirmed",)] if mode == "accepted" else []), "delete outcome not recorded")
        assert payload.exists() and not mock.adds, "task absence bypassed file evidence"
        charges = app.rows("SELECT charge_json FROM racing_reclaim_charges")
        reception["reclaimEnabled"] = False
        app.request(f"/api/racing/reception-policies/{instance}", reception, method="PUT", expected=204)
        app.restore_backup()
        until(lambda: mock.rid >= 3, "snapshot did not recover")
        time.sleep(7)
        assert mock.deletes == 1 and not mock.adds
        payload.unlink()
        if mode == "accepted":
            fixture["confirmed"](app)
            assert app.rows("SELECT state FROM racing_reclaim_releases") == [("observed",)]
            assert mock.adds == [incoming["id"]]
        else:
            time.sleep(12)
            assert app.rows("SELECT state FROM automatic_delete_intents") == [("unknown",)]
            assert app.rows("SELECT state FROM racing_reclaim_releases") == [("pending",)]
            assert not mock.adds, "unknown deletion was automatically released"
        assert app.rows("SELECT charge_json FROM racing_reclaim_charges") == charges
        assert mock.deletes == 1 and not mock.errors
        print(f"PASS native reclaim {mode}: explicit permission, one delete, consistent backup restore, file/space gate, persistent charges", flush=True)


def main():
    parent = os.environ.get("QUI_RECLAIM_TEST_ROOT")
    if sys.platform != "linux" or not parent:
        print("SKIP native reclaim: requires Linux and QUI_RECLAIM_TEST_ROOT on an isolated supported filesystem")
        return
    for mode in ("accepted", "disconnect"):
        with tempfile.TemporaryDirectory(dir=parent, prefix="qui-reclaim-") as root:
            run(mode, root)


if __name__ == "__main__":
    main()
