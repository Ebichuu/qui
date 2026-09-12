#!/usr/bin/env python3
"""Observe synthetic official reclaim candidates; never send delete requests."""
from pathlib import Path
import runpy
import time

from racing_smoke import App, until

fixture = runpy.run_path(str(Path(__file__).with_name("smoke-racing-integration.py")))


def main():
    item = fixture["torrent"](710)
    with App() as app, fixture["Fixture"](item) as mock:
        mock.show(item)
        with mock.lock:
            mock.tasks[item["hash"]].update(added_on=100, completed=item["size"], amount_left=0, state="stalledUP", dlspeed=0, upspeed=1)
        instance = app.request("/api/instances", dict(name="Synthetic reclaim observer", host=mock.origin, username="synthetic", password="synthetic"), expected=201)["id"]
        until(lambda: mock.rid >= 2, "shared snapshot did not warm")
        path = f"/api/instances/{instance}/automations"
        rule = dict(name="Synthetic official-only condition", trackerPattern="*", enabled=True, dryRun=False, notify=False, intervalSeconds=60, conditions={"schemaVersion":"1", "delete":{"usage":"official","enabled":True,"mode":"deleteWithFiles","dailyTrigger":{"field":"FREE_SPACE","operator":"LESS_THAN","value":"1"},"conditionMatchDurationSeconds":60,"condition":{"field":"UP_SPEED","operator":"LESS_THAN","value":"10"}}})
        saved = app.request(path, rule, expected=201)
        policy = dict(enabled=True, ruleIds=[saved["id"]], maxDeletes=1, maxReclaimBytes=2**30, maxRecentUploadBytes=1000, recentUploadWindowSeconds=3600, maxOvershootBytes=2**30)
        app.request(f"/api/racing/reclaim-settings/instances/{instance}", policy, method="PUT", expected=204)
        result = app.request(path + "/reclaim-candidates")
        assert not result["candidates"] and not result["unavailableRuleIds"]
        app.request(path + "/apply", {}, expected=202)
        assert not app.rows("SELECT * FROM automatic_delete_intents"), "official-only rule entered daily delete"
        start = time.monotonic()
        while time.monotonic() - start < 62:
            time.sleep(2)
            result = app.request(path + "/reclaim-candidates")
        assert [c["hash"] for c in result["candidates"]] == [item["hash"]], result
        before = app.rows("SELECT elapsed_ns FROM reclaim_condition_observations")[0][0]
        previous_rid = mock.rid
        app.restart(crash=True)
        until(lambda: mock.rid >= previous_rid + 2, "snapshot did not recover")
        result = app.request(path + "/reclaim-candidates")
        after = app.rows("SELECT elapsed_ns FROM reclaim_condition_observations")[0][0]
        assert after >= before and after - before < 2_000_000_000, "restart gap credited"
        assert len(result["candidates"]) == 1
        with mock.lock:
            mock.tasks[item["hash"]]["added_on"] = 200
        previous_rid = mock.rid
        until(lambda: mock.rid >= previous_rid + 2, "new generation not observed")
        result = app.request(path + "/reclaim-candidates")
        assert not result["candidates"], "new generation inherited maturity"
        assert not app.rows("SELECT * FROM automatic_delete_intents")
        assert not mock.adds and not mock.errors, "candidate observation mutated downloader"
        print("PASS C10: official-only candidates mature despite false daily trigger; restart retains measured time; new generation resets; no downloader actions")


if __name__ == "__main__":
    main()
