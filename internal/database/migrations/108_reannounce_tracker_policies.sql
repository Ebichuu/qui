-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later
CREATE TABLE reannounce_tracker_policies (
 instance_id INTEGER NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
 tracker_key TEXT NOT NULL,
 site_id INTEGER NOT NULL REFERENCES racing_sites(id) ON DELETE RESTRICT,
 tracker_host TEXT NOT NULL,
 interval_seconds INTEGER NOT NULL,
 wait_message_digest TEXT NOT NULL,
 wait_seconds INTEGER NOT NULL,
 delete_protection TEXT NOT NULL,
 PRIMARY KEY(instance_id,tracker_key)
);
CREATE INDEX idx_reannounce_policy_site ON reannounce_tracker_policies(site_id);
CREATE TABLE reannounce_tracker_waits (
 instance_id INTEGER NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
 torrent_hash TEXT NOT NULL,
 added_on BIGINT NOT NULL,
 tracker_key TEXT NOT NULL,
 message_digest TEXT NOT NULL,
 started_ns BIGINT NOT NULL,
 observed_ns BIGINT NOT NULL,
 not_before_ns BIGINT NOT NULL,
 PRIMARY KEY(instance_id,torrent_hash,added_on,tracker_key)
);
