#!/usr/bin/env python3
"""Synthetic downloader: automatic deletion is sent once across restart."""
from pathlib import Path
import runpy
import socket
import time

from racing_smoke import App, until

fixture = runpy.run_path(str(Path(__file__).with_name("smoke-racing-integration.py")))


class DeleteHandler(fixture["Handler"]):
    def do_POST(self):
        if not self.path.endswith("/torrents/delete"):
            return super().do_POST()
        self.rfile.read(int(self.headers.get("Content-Length", 0)))
        f = self.server.fixture
        f.deletes += 1
        if f.delete_mode == "disconnect":
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return
        # Keep the task visible to exercise delayed qB confirmation.
        self.respond("Ok.")


def main():
    for mode in ("accepted", "disconnect"):
        item = fixture["torrent"](950)
        with App() as app, fixture["Fixture"](item) as mock:
            mock.server.RequestHandlerClass = DeleteHandler
            mock.deletes, mock.delete_mode = 0, mode
            mock.show(item)
            mock.tasks[item["hash"]]["added_on"] = 100
            instance = app.request("/api/instances", dict(name="Synthetic delete owner", host=mock.origin, username="synthetic", password="synthetic"), expected=201)["id"]
            until(lambda: mock.rid >= 2, "snapshot did not warm")
            path = f"/api/instances/{instance}/automations"
            app.request(path, dict(name="Synthetic deletion", trackerPattern="*", enabled=True, dryRun=False, intervalSeconds=60, conditions={"schemaVersion":"1", "delete":{"enabled":True,"mode":"delete","condition":{"field":"UP_SPEED","operator":"LESS_THAN","value":"10"}}}), expected=201)
            app.request(path+"/apply", {}, expected=202)
            until(lambda: mock.deletes == 1, "initial delete not sent")
            expected = "accepted" if mode == "accepted" else "unknown"
            until(lambda: app.rows("SELECT state FROM automatic_delete_intents") == [(expected,)], "result not recorded")
            for _ in range(2):
                app.request(path+"/apply", {}, expected=202)
            assert mock.deletes == 1
            previous = mock.rid
            app.restore_backup()
            until(lambda: mock.rid >= previous+2, "restarted snapshot did not warm")
            app.request(path+"/apply", {}, expected=202)
            assert mock.deletes == 1, "automatic delete replayed after restart"
            assert not mock.errors and not mock.adds
            print(f"PASS automatic delete {mode}: one request, persistent ownership, no backup-restore replay")


if __name__ == "__main__":
    main()
