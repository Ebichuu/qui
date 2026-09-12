#!/usr/bin/env python3
"""A concrete synthetic official event records its deficit, without deleting."""
from pathlib import Path
import runpy
import time

from racing_smoke import App, until

fixture = runpy.run_path(str(Path(__file__).with_name("smoke-racing-integration.py")))


def main():
    item = fixture["torrent"](720, 10 * fixture["MIB"])
    with App() as app, fixture["Fixture"](item, free=fixture["MIB"]) as mock:
        instance, _ = fixture["downloader"](app, mock)
        fixture["sources"](app, mock, instance)
        config = app.request("/api/racing/configuration")
        rule = config["rules"][0]
        payload = {key: rule[key] for key in ["name", "enabled", "sourceIds", "acceptKinds", "receiveWindowSeconds", "targetInstanceId"]}
        payload["allowOfficialReclaim"] = True
        app.request(f'/api/racing/rules/{rule["id"]}', payload, method="PUT")
        workflow = app.request(f"/api/instances/{instance}/automations", dict(name="Synthetic official condition", trackerPattern="*", enabled=True, dryRun=False, conditions={"schemaVersion":"1","delete":{"usage":"official","enabled":True,"mode":"deleteWithFiles","conditionMatchDurationSeconds":60,"condition":{"field":"UP_SPEED","operator":"LESS_THAN","value":"10"}}}), expected=201)
        policy = dict(enabled=True, ruleIds=[workflow["id"]], maxDeletes=2, maxReclaimBytes=100*fixture["MIB"], maxRecentUploadBytes=0, recentUploadWindowSeconds=3600, maxOvershootBytes=20*fixture["MIB"])
        app.request(f"/api/racing/reclaim-settings/instances/{instance}", policy, method="PUT", expected=204)
        assert app.request("/api/racing/reclaim-assessments") == [], "assessment without a concrete event"
        mock.publish("/rss", item)
        rows = until(lambda: app.request("/api/racing/reclaim-assessments"), "official deficit was not assessed", 60)
        result = rows[0]["assessment"]
        assert result["deficitBytes"] == 9*fixture["MIB"], result
        assert result["state"] == "insufficient_evidence_or_budget" and not result["selected"], result
        assert not app.rows("SELECT * FROM automatic_delete_intents") and not mock.adds and not mock.errors
        app.restart(crash=True)
        recovered = app.request("/api/racing/reclaim-assessments")
        assert recovered[0]["candidateKey"] == rows[0]["candidateKey"]
        with mock.lock:
            mock.free = 20*fixture["MIB"]
        fixture["confirmed"](app)
        assert mock.adds == [item["id"]], "normal addition did not recover after actual space appeared"
        assert not app.rows("SELECT * FROM automatic_delete_intents") and not mock.errors
        print("PASS C11 assessment: concrete official deficit persisted; insufficient evidence sends no delete; restart retains audit; actual free space resumes normal add")


if __name__ == "__main__":
    main()
