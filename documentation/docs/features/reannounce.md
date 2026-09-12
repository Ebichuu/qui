---
sidebar_position: 4
title: Reannounce
description: Automatically fix stalled torrents by reannouncing to trackers.
---

# Tracker Reannounce

qui reannounces stalled torrents to trackers. If a tracker fails to register a new upload immediately, qui helps your torrents start seeding without manual work.

qBittorrent does not retry failed announces quickly. If a tracker registers a new upload slowly or returns an error, the torrent stays stalled. qui retries the announce for you.

qui never spams trackers. If a tracker update is in progress or a response is pending, qui waits. qui acts only after the tracker responds and a problem exists.

## Quick start

1. Open **Automations** in the main navigation.
2. Find your instance in the **Reannounce** card.
3. Turn on the switch next to the instance name. In the dialog that opens, click **Enable**.
4. If you want to change settings, expand the instance row and click **Configure**. Adjust the values and click **Save Changes**.

qui now monitors stalled torrents in the background.

## Configuration

### Timing

| Setting | Description | Default |
|---------|-------------|---------|
| Initial Wait | Time to wait after you add a torrent before qui checks it | 15s |
| Retry Interval | Time between retries within a single reannounce attempt | 7s |
| Max Torrent Age | Stop monitoring torrents older than this | 10 minutes |
| Max Retries | Maximum consecutive retries within a single scan cycle | 50 |

Some slow trackers need up to 50 retries at 7s intervals (about 6 minutes) to register uploads.

### Monitoring scope

Choose which torrents qui monitors:

- **Monitor All Stalled Torrents**: qui checks every stalled torrent. This is the default setting for new instances. If you want to ignore specific categories, tags, or trackers (such as public trackers), add **Exclude** rules.
- **Custom filter (Monitor All disabled)**: qui checks only torrents that match your **Include** rules. **Exclude** rules still block specific items within those groups.

If you disable **Monitor All** and add no **Include** rules, no torrent can match. qui then does no scan and sends no request to qBittorrent.

### Quick Retry

By default, qui waits about **2 minutes** between reannounce attempts for the same torrent. This duration acts as a per-torrent cooldown between scans.

If you enable **Quick Retry**, qui uses the **Retry Interval** (default 7s) as the cooldown instead. Stalled torrents then recover faster. The **Retry Interval** controls the spacing of retries inside each scan attempt. If you enable **Quick Retry**, it also controls the cooldown between scans.

Quick Retry helps on trackers that register new uploads slowly. Some sites need time before they recognize a new torrent, which causes initial stalls.

## Activity log

If you want to view activity:

1. Open **Automations** and find your instance in the **Reannounce** card.
2. Expand the instance row, click **Configure**, then select the **Activity Log** tab.

The log displays a real-time feed of every checked torrent. It shows whether qui succeeded, failed, or skipped the reannounce, for example because the tracker already works.

A retry succeeds only after qB reports the original enabled Trackers as working. Accepting the reannounce request alone does not count as success, and an exhausted retry remains unconfirmed. If only some Trackers recover, qui stops further torrent-wide retries and records a skip. Changing the Tracker list also stops that attempt. These observations do not prove that a site's account statistics have refreshed.

Tracker queries made during these jobs also save a historical snapshot, available at `GET /api/instances/{instanceID}/reannounce/observations`. It contains separate Tracker and local-counter observation times and survives restarts. The endpoint returns at most 100 tasks; it does not cover every torrent or represent live state. Full Tracker URLs and messages are stored as digests. The local upload counter is not a site-accounted amount.

Automation actions, bulk actions, and proxy requests use the same monitoring scope and job queue. Matching torrents keep their initial wait and pending-Tracker checks; a successful request can mean that the task is waiting or already queued. Tasks outside the monitoring scope retain direct qB behavior unless an account policy applies. Within monitoring jobs, the send interval is stored before each request and survives restart. Shortening the setting does not cancel an interval already recorded; increasing it also applies to the previous send. Account policies are configured separately through the API below.


## Tracker account policies (development API)

`GET` and `PUT /api/instances/{instanceID}/reannounce/tracker-policies` list and upsert policies by exact full Tracker URL digest. There is no settings form yet. Copy the `key`, `host` and, where applicable, `messageDigest` from a verified observation; identify the correct account locally before saving. Never publish full announce URLs or messages containing credentials. A host alone cannot identify an account.

Each policy requires `trackerKey`, `trackerHost`, an enabled `siteId` whose configured hosts include that host, and `intervalSeconds` (1–2,592,000). `waitMessageDigest` plus `waitSeconds` configures one explicitly recognized message and its known wait; use an empty digest and zero seconds when no message is configured. Unrecognized errors do not acquire an inferred site-specific wait. Policies can be updated but do not yet have a removal endpoint. Disabling their site or removing its host blocks policy evaluation until the binding is repaired.

An exact binding, or an unrecognized account on a configured host, makes the task subject to complete Tracker coverage. Every real Tracker affected by qB's torrent-wide request must have an exact binding. A previously observed bound Tracker disappearing, an unknown task generation, pending response, disabled Tracker, or an already working Tracker stops reannounce. Exclusion from instance monitoring does not bypass account policies. Existing monitored jobs still follow their own filters and retry limits; this does not provide independent per-Tracker requests.

Known waits survive restart. Repeated observation of the same response within one send does not slide the deadline. A changed response or a later send may start a new known wait, and shortening configuration does not cancel an existing deadline. Sending uses the longest applicable interval and one HTTP attempt. External changes between observations are not continuously observable; qB does not provide a conditional send tied to a task generation.

`deleteProtection` is explicit:

- `blocked`: retain automatic deletion protection.
- `accounted`: retain protection because a site-accounting evidence source is not implemented.
- `reported_working`: accept fresh qB working state for every bound Tracker after waits expire. This does **not** prove that the site has credited upload.

`GET /api/instances/{instanceID}/reannounce/deletion-check?hash=...&addedOn=...` queries the task and Trackers, records known waits and returns the current constraints. Its result is not a reservation for later deletion. Daily automation and failed-export cleanup check again before claiming an automatic deletion. Unmanaged daily tasks retain their existing behavior; official reclaim assessments require complete policy coverage. Manual and proxy deletion remain external actions. Official reclaim still does not execute deletions: its persistent execution plan and released-space verification are pending.

The development checks use two synthetic Trackers and a real isolated application process. Two real site accounts and their accounting behavior remain unverified.
