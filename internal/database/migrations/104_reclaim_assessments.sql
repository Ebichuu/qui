-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later
CREATE TABLE racing_reclaim_assessments (
 candidate_key TEXT NOT NULL REFERENCES racing_candidates(candidate_key) ON DELETE CASCADE,
 instance_id INTEGER NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
 configuration_revision BIGINT NOT NULL,
 assessment_json TEXT NOT NULL,
 observed_at TEXT NOT NULL,
 PRIMARY KEY(candidate_key, instance_id)
);
CREATE INDEX idx_reclaim_assessment_instance ON racing_reclaim_assessments(instance_id);
