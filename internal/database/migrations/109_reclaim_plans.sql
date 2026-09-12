-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later
-- Keep budgets even if discovery history or configuration objects are removed.
CREATE TABLE racing_reclaim_plans (
 candidate_key TEXT PRIMARY KEY,
 instance_id INTEGER NOT NULL,
 plan_json TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX idx_reclaim_plans_instance ON racing_reclaim_plans(instance_id);
CREATE TABLE racing_reclaim_charges (
 operation_id TEXT PRIMARY KEY,
 candidate_key TEXT NOT NULL REFERENCES racing_reclaim_plans(candidate_key) ON DELETE RESTRICT,
 charge_json TEXT NOT NULL
);
CREATE INDEX idx_reclaim_charges_candidate ON racing_reclaim_charges(candidate_key);
CREATE INDEX idx_automatic_delete_canonical_hash ON automatic_delete_intents(instance_id,LOWER(torrent_hash));
