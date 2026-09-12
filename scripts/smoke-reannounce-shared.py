#!/usr/bin/env python3
"""Exercise all reannounce entry points against one isolated synthetic task."""
from pathlib import Path
import runpy
import time
import urllib.request
from racing_smoke import App, until

fixture = runpy.run_path(str(Path(__file__).with_name("smoke-reannounce-result.py")))
base = fixture["fixture"]


def main():
    item = base["torrent"](740)
    mock = base["Fixture"](item)
    mock.server.RequestHandlerClass = fixture["Handler"]
    mock.mode, mock.posts = "failed", 0
    mock.show(item)
    mock.tasks[item["hash"]].update(added_on=int(time.time()),state="stalledUP",dlspeed=0)
    settings = dict(enabled=True,monitorAll=True,initialWaitSeconds=60,reannounceIntervalSeconds=20,maxAgeSeconds=600,maxRetries=1)
    with App() as app, mock:
        instance = app.request("/api/instances",dict(name="Synthetic shared reannounce",host=mock.origin,username="synthetic",password="synthetic",reannounceSettings=settings),expected=201)["id"]
        until(lambda: mock.rid >= 2,"snapshot did not warm")
        key = app.request("/api/client-api-keys",dict(clientName="Synthetic reannounce caller",instanceId=instance))
        def proxy(hashes=item["hash"]):
            request = urllib.request.Request(app.base+key["proxyUrl"]+"/api/v2/torrents/reannounce",data=("hashes="+hashes).encode(),headers={"Content-Type":"application/x-www-form-urlencoded"})
            with app.opener.open(request,timeout=5) as response:
                assert response.status == 200
        def bulk():
            app.request(f"/api/instances/{instance}/torrents/bulk-action",dict(action="reannounce",hashes=[item["hash"]]))
        workflow = app.request(f"/api/instances/{instance}/automations",dict(name="Synthetic reannounce rule",trackerPattern="*",enabled=True,conditions={"schemaVersion":"1","reannounce":{"enabled":True,"condition":{"field":"UP_SPEED","operator":"LESS_THAN","value":"10"}}}),expected=201)
        def automate():
            app.request(f"/api/instances/{instance}/automations/apply",{},expected=202)
        bulk(); proxy("all"); automate()
        time.sleep(3)
        assert mock.posts == 0,"initial wait bypassed"
        with mock.lock:
            mock.tasks[item["hash"]]["added_on"] = int(time.time())-120
        previous = mock.rid
        until(lambda: mock.rid >= previous+2,"mature generation not observed")
        bulk(); proxy(); automate()
        until(lambda: mock.posts == 1,"shared job did not send",35)
        assert len(app.rows("SELECT * FROM reannounce_attempts")) == 1
        # Shortening settings cannot remove the original persisted deadline,
        # including after job memory disappears on restart.
        settings["reannounceIntervalSeconds"] = 1
        app.request(f"/api/instances/{instance}",dict(name="Synthetic shared reannounce",host=mock.origin,username="synthetic",reannounceSettings=settings),method="PUT")
        app.restart(crash=True)
        bulk(); proxy(); automate()
        time.sleep(3)
        assert mock.posts == 1,"restart or another entry point bypassed shared interval"
        app.request(f"/api/instances/{instance}/automations/{workflow['id']}",method="DELETE",expected=204)
        settings.update(excludeTags=True,tags=["external"])
        with mock.lock:
            mock.tasks[item["hash"]]["tags"] = "external"
        app.request(f"/api/instances/{instance}",dict(name="Synthetic shared reannounce",host=mock.origin,username="synthetic",reannounceSettings=settings),method="PUT")
        previous = mock.rid
        until(lambda: mock.rid >= previous+2,"excluded scope not observed")
        bulk(); proxy()
        assert mock.posts == 3,"outside-scope behavior changed"
        assert not mock.errors,mock.errors
    print("PASS C12 shared dispatch: bulk, proxy and automation respect initial wait; one shared send; restart retains interval; excluded scope stays direct")


if __name__ == "__main__":
    main()
