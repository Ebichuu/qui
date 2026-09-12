-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later
-- Keep the baseline and pool lease until both task absence and capacity recover.
CREATE TABLE racing_reclaim_releases (
 operation_id TEXT PRIMARY KEY REFERENCES racing_reclaim_charges(operation_id) ON DELETE RESTRICT,
 pool_id INTEGER NOT NULL,
 baseline_json TEXT NOT NULL,
 observation_json TEXT,
 state TEXT NOT NULL CHECK(state IN ('pending','observed')),
 updated_at TEXT NOT NULL
);
CREATE INDEX idx_reclaim_release_pool_state ON racing_reclaim_releases(pool_id,state,updated_at);
-- Old unresolved operations have no trustworthy baseline. Preserve their pool
-- occupancy as unknown; never manufacture pre-delete capacity on upgrade.
INSERT INTO racing_reclaim_releases(operation_id,pool_id,baseline_json,state,updated_at)
SELECT c.operation_id,CAST(json_extract(p.plan_json, '$.poolId') AS INTEGER),'null','pending',p.updated_at
FROM racing_reclaim_charges c JOIN racing_reclaim_plans p ON p.candidate_key=c.candidate_key
WHERE json_extract(p.plan_json, '$.state')='awaiting_release';
