#!/usr/bin/env python3
"""Exercise C10 configuration inheritance in an isolated, real qui process."""
from pathlib import Path
import runpy

from racing_smoke import App

fixture = runpy.run_path(str(Path(__file__).with_name("smoke-racing-integration.py")))


def main():
    with App() as app, fixture["Fixture"](fixture["torrent"](800)) as mock:
        instance = app.request("/api/instances", dict(name="Synthetic reclaim settings", host=mock.origin, username="synthetic", password="synthetic"), expected=201)["id"]
        rule_path = f"/api/instances/{instance}/automations"
        rule = app.request(rule_path, dict(name="Synthetic daily rule", trackerPattern="*", enabled=False, dryRun=True, conditions={"schemaVersion":"1", "delete":{"enabled":True,"mode":"delete","conditionMatchDurationSeconds":600,"condition":{"field":"UP_SPEED","operator":"LESS_THAN","value":"10"}}}), expected=201)
        groups = [app.request("/api/racing/groups", dict(name=f"Synthetic group {n}", enabled=True, instanceIds=[instance]), expected=201)["id"] for n in range(2)]
        path = "/api/racing/reclaim-settings"
        policy = dict(enabled=True, ruleIds=[rule["id"], rule["id"]], maxDeletes=2, maxReclaimBytes=100, maxRecentUploadBytes=0, recentUploadWindowSeconds=3600, maxOvershootBytes=10)
        def state():
            return app.request(path)["effective"][0]
        assert state()["state"] == "unconfigured"
        for group in groups:
            app.request(f"{path}/groups/{group}", policy, method="PUT", expected=204)
        assert state()["state"] == "inherited"
        assert state()["policy"]["ruleIds"] == [rule["id"]]
        policy["maxDeletes"] = 3
        app.request(f"{path}/groups/{groups[1]}", policy, method="PUT", expected=204)
        assert state()["state"] == "conflict" and "policy" not in state()
        disabled = dict(policy, enabled=False)
        app.request(f"{path}/instances/{instance}", disabled, method="PUT", expected=204)
        assert state()["state"] == "disabled"
        app.restart(crash=True)
        assert state()["state"] == "disabled"
        app.request(f"{rule_path}/{rule['id']}", method="DELETE", expected=409)
        app.request(f"{path}/instances/{instance}", method="DELETE", expected=204)
        assert state()["state"] == "conflict"
        for group in groups:
            app.request(f"{path}/groups/{group}", method="DELETE", expected=204)
        app.request(f"{rule_path}/{rule['id']}", method="DELETE", expected=204)
        assert state()["state"] == "unconfigured"
        assert not mock.adds and not mock.errors
        print("PASS C10 settings: inheritance, conflict, explicit disable, crash recovery, reference protection; no downloader mutations")


if __name__ == "__main__":
    main()
