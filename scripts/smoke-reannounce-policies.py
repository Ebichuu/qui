#!/usr/bin/env python3
"""Verify account constraints with an isolated app and two synthetic trackers."""
import hashlib
from pathlib import Path
import runpy
import urllib.parse

from racing_smoke import App, until

fixture = runpy.run_path(str(Path(__file__).with_name("smoke-reannounce-result.py")))
base = fixture["fixture"]


def digest(value):
    return hashlib.sha256(value.encode()).hexdigest()


class Handler(fixture["Handler"]):
    def do_GET(self):
        mock = self.server.fixture
        if urllib.parse.urlparse(self.path).path.endswith("/torrents/trackers"):
            with mock.lock:
                return self.respond(mock.trackers)
        return super().do_GET()

    def do_POST(self):
        if self.path.endswith("/torrents/delete"):
            self.rfile.read(int(self.headers.get("Content-Length", 0)))
            with self.server.fixture.lock:
                self.server.fixture.deletes += 1
            return self.respond("Ok.")
        return super().do_POST()


def main():
    item = base["torrent"](760)
    mock = base["Fixture"](item)
    mock.server.RequestHandlerClass = Handler
    mock.mode, mock.posts, mock.deletes = "failed", 0, 0
    mock.trackers = [dict(url=f"https://{site}.example.invalid/announce", status=4, msg="Known synthetic wait") for site in ("a", "b")]
    mock.show(item)
    mock.tasks[item["hash"]].update(added_on=100, state="stalledUP", dlspeed=0)
    with App() as app, mock:
        instance = app.request("/api/instances", dict(name="Synthetic account constraints", host=mock.origin, username="synthetic", password="synthetic", reannounceSettings=dict(enabled=False)), expected=201)["id"]
        until(lambda: mock.rid >= 2, "snapshot did not warm")
        path = f"/api/instances/{instance}/reannounce"
        check = path + "/deletion-check?hash=" + item["hash"] + "&addedOn=100"
        policies = []
        for tracker in mock.trackers:
            host = urllib.parse.urlparse(tracker["url"]).hostname
            site = app.request("/api/racing/sites", dict(name="Synthetic " + host, baseUrl=mock.origin, enabled=True, trackerHosts=[host], requestIntervalSeconds=1), expected=201)["id"]
            policy = dict(trackerKey=digest(tracker["url"]), siteId=site, trackerHost=host, intervalSeconds=20, waitMessageDigest=digest("Known synthetic wait"), waitSeconds=2, deleteProtection="accounted")
            app.request(path + "/tracker-policies", policy, method="PUT", expected=204)
            policies.append(policy)
        assert len(app.request(path + "/tracker-policies")) == 2
        def bulk():
            app.request(f"/api/instances/{instance}/torrents/bulk-action", dict(action="reannounce", hashes=[item["hash"]]))
        bulk()
        assert mock.posts == 0, "known wait bypassed outside instance monitoring"
        assert not app.request(check)["deleteAllowed"]
        deadlines = app.rows("SELECT tracker_key,not_before_ns FROM reannounce_tracker_waits ORDER BY tracker_key")
        app.restart(crash=True)
        assert deadlines == app.rows("SELECT tracker_key,not_before_ns FROM reannounce_tracker_waits ORDER BY tracker_key")
        until(lambda: app.request(check)["reannounceAllowed"], "known wait did not expire")
        bulk()
        assert mock.posts == 1, "account policies did not permit a send after wait"
        for policy in policies:
            policy.update(intervalSeconds=1, waitSeconds=0, waitMessageDigest="")
            app.request(path + "/tracker-policies", policy, method="PUT", expected=204)
        app.restart(crash=True)
        with mock.lock:
            for tracker in mock.trackers:
                tracker.update(status=2, msg="")
        bulk()
        assert mock.posts == 1, "shorter settings bypassed persisted interval"
        assert not app.request(check)["deleteAllowed"], "accounted policy inferred site accounting"
        automation_path = f"/api/instances/{instance}/automations"
        rule = app.request(automation_path, dict(name="Synthetic protected deletion", trackerPattern="*", enabled=True, dryRun=False, intervalSeconds=60, conditions={"schemaVersion":"1", "delete":{"enabled":True,"mode":"delete","condition":{"field":"UP_SPEED","operator":"LESS_THAN","value":"10"}}}), expected=201)
        app.request(automation_path + "/apply", {}, expected=202)
        activities = until(lambda: app.request(automation_path + "/activity"), "protected delete was not evaluated")
        assert any(row["outcome"] == "failed" and "blocked by tracker protection" in row["reason"] for row in activities), activities
        assert mock.deletes == 0 and not app.rows("SELECT * FROM automatic_delete_intents"), "automatic delete bypassed protection"
        app.request(automation_path + f"/{rule['id']}", method="DELETE", expected=204)
        for policy in policies:
            policy["deleteProtection"] = "reported_working"
            app.request(path + "/tracker-policies", policy, method="PUT", expected=204)
        assert not app.request(check)["deleteAllowed"], "delete bypassed original send interval"
        until(lambda: app.request(check)["deleteAllowed"], "explicit working-state condition did not release", 25)
        with mock.lock:
            mock.trackers[0]["url"] += "?account=another-synthetic-account"
        result = app.request(check)
        assert not result["deleteAllowed"] and not result["reannounceAllowed"], result
        bulk()
        assert mock.posts == 1, "changed account escaped constraints"
        assert not mock.errors, mock.errors
    print("PASS C12 account policies: two exact bindings, known wait, restart, original interval, automatic deletion guard, explicit working-state condition and changed-account rejection")


if __name__ == "__main__":
    main()
