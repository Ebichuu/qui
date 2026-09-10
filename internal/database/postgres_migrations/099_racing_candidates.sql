-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later

ALTER TABLE racing_discoveries ADD COLUMN evaluated_revision INTEGER NOT NULL DEFAULT 0;
CREATE TABLE racing_candidates (
    candidate_key TEXT PRIMARY KEY,
    site_id INTEGER NOT NULL,
    event_key TEXT NOT NULL,
    source_scope INTEGER NOT NULL DEFAULT 0,
    first_seen_at TEXT NOT NULL,
    candidate_json TEXT NOT NULL,
    selection_json TEXT NOT NULL,
    state TEXT NOT NULL,
    next_evaluation_at TEXT,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_racing_candidates_due ON racing_candidates(next_evaluation_at);

CREATE TABLE racing_candidate_metadata (
    candidate_key TEXT PRIMARY KEY REFERENCES racing_candidates(candidate_key) ON DELETE CASCADE,
    source_id INTEGER NOT NULL,
    public_metadata TEXT NOT NULL,
    private_ciphertext TEXT NOT NULL,
    observed_at TEXT NOT NULL
);
