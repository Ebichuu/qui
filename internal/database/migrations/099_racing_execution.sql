-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later

CREATE TABLE racing_instance_policies (
    instance_id INTEGER PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
    enabled INTEGER NOT NULL DEFAULT 0,
    max_concurrent_adds INTEGER NOT NULL,
    max_active_downloads INTEGER NOT NULL,
    min_free_bytes INTEGER NOT NULL,
    save_path TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    auto_tmm INTEGER NOT NULL DEFAULT 0,
    start_paused INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
CREATE TABLE racing_add_intents (
    candidate_key TEXT PRIMARY KEY,
    instance_id INTEGER NOT NULL,
    hash_v1 TEXT NOT NULL DEFAULT '',
    hash_v2 TEXT NOT NULL DEFAULT '',
    plan_json TEXT NOT NULL,
    metainfo_ciphertext TEXT NOT NULL,
    state TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    reserved_at TEXT NOT NULL,
    submitted_at TEXT,
    accepted_at TEXT,
    confirmed_at TEXT,
    runnable_at TEXT,
    transferred_at TEXT,
    observed_at TEXT,
    missing_since TEXT,
    missing_observed_at TEXT,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_racing_intents_instance_state ON racing_add_intents(instance_id,state);
CREATE TABLE racing_space_commitments (
    candidate_key TEXT NOT NULL REFERENCES racing_add_intents(candidate_key) ON DELETE CASCADE,
    storage_pool_id INTEGER NOT NULL REFERENCES racing_storage_pools(id) ON DELETE RESTRICT,
    bytes INTEGER NOT NULL CHECK (bytes >= 0),
    PRIMARY KEY(candidate_key,storage_pool_id)
);
CREATE INDEX idx_racing_commitments_pool ON racing_space_commitments(storage_pool_id);

-- All configuration and intent writes lock this row before other rows.
CREATE TABLE racing_configuration_revision (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision BIGINT NOT NULL
);
INSERT INTO racing_configuration_revision(id, revision) VALUES (1, 0);

ALTER TABLE racing_candidate_metadata ADD COLUMN source_revision TEXT NOT NULL DEFAULT '';
ALTER TABLE racing_candidate_metadata ADD COLUMN site_revision TEXT NOT NULL DEFAULT '';
