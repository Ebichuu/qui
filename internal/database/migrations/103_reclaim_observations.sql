-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later

CREATE TABLE reclaim_condition_observations (
    instance_id INTEGER NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    rule_id INTEGER NOT NULL REFERENCES automations(id) ON DELETE CASCADE,
    rule_version TEXT NOT NULL,
    torrent_hash TEXT NOT NULL,
    added_on BIGINT NOT NULL,
    dry_run INTEGER NOT NULL,
    elapsed_ns BIGINT NOT NULL CHECK (elapsed_ns >= 0),
    duration_ns BIGINT NOT NULL CHECK (duration_ns > 0),
    observed_at TEXT NOT NULL,
    uploaded BIGINT NOT NULL,
    downloaded BIGINT NOT NULL,
    PRIMARY KEY (instance_id,rule_id,torrent_hash,added_on,dry_run)
);
CREATE INDEX idx_reclaim_observation_rule ON reclaim_condition_observations(rule_id);
