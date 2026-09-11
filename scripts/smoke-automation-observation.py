#!/usr/bin/env python3
"""C10 observation checkpoint/restart using a synthetic downloader; no deletes."""
from pathlib import Path
import runpy
import time

from racing_smoke import App, until

fixture = runpy.run_path(str(Path(__file__).with_name("smoke-racing-integration.py")))


def main():
    item = fixture["torrent"](700)
    with App() as app, fixture["Fixture"](item) as mock:
        mock.show(item)
        instance = app.request("/api/instances", dict(name="Synthetic C10 observer", host=mock.origin, username="synthetic", password="synthetic"), expected=201)["id"]
        until(lambda: mock.rid >= 2, "shared snapshot did not warm")
        path = f"/api/instances/{instance}/automations"
        rule = dict(name="Synthetic continuous observation", trackerPattern="*", enabled=True, dryRun=True, notify=False, intervalSeconds=60, conditions={"schemaVersion":"1", "delete":{"enabled":True,"mode":"deleteWithFiles","dailyTrigger":{"field":"FREE_SPACE","operator":"LESS_THAN","value":"1"},"conditionMatchDurationSeconds":600,"condition":{"field":"UP_SPEED","operator":"LESS_THAN","value":"10"}}})
        saved = app.request(path,rule,expected=201)
        assert saved["conditions"]["delete"]["dailyTrigger"] == rule["conditions"]["delete"]["dailyTrigger"]
        preview = app.request(path+"/preview", rule)
        assert preview["totalMatches"] == 0, "daily preview ignored the false trigger"
        def apply():
            app.request(path+"/apply", {}, expected=202)
        apply()
        until(lambda: bool(app.rows("SELECT elapsed_ns FROM automation_condition_observations")), "observation not persisted")
        time.sleep(2)
        apply()
        before = app.rows("SELECT elapsed_ns FROM automation_condition_observations")[0][0]
        assert before > 0
        app.stop(crash=True)
        time.sleep(3)
        previous_rid = mock.rid
        app.start()
        app.request("/api/auth/login",app.credentials)
        until(lambda: mock.rid >= previous_rid + 2, "restarted snapshot did not warm")
        time.sleep(2)
        apply()
        after = app.rows("SELECT elapsed_ns FROM automation_condition_observations")[0][0]
        assert after == before, "restart gap was credited as observation"
        assert not mock.adds and not mock.errors, "observer unexpectedly mutated downloader"
        print("PASS C10: false daily trigger preserves observation; preview blocks delete; restart excludes downtime; no downloader mutations")


if __name__ == "__main__":
    main()
