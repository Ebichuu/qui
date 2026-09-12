-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later
CREATE TABLE reannounce_observations (
 instance_id INTEGER NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
 torrent_hash TEXT NOT NULL,
 added_on BIGINT NOT NULL,
 observed_ns BIGINT NOT NULL,
 local_observed_ns BIGINT NOT NULL,
 local_uploaded BIGINT NOT NULL CHECK(local_uploaded >= 0),
 trackers_json TEXT NOT NULL,
 PRIMARY KEY(instance_id,torrent_hash)
);
CREATE INDEX idx_reannounce_observations_time ON reannounce_observations(instance_id,observed_ns);
