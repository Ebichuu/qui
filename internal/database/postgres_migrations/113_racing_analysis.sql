-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later
CREATE TABLE racing_analysis_settings (
    instance_id INTEGER PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
    enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1))
);
CREATE TABLE racing_analysis_history (
    candidate_key TEXT PRIMARY KEY REFERENCES racing_add_intents(candidate_key) ON DELETE CASCADE,
    report_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_racing_analysis_history_updated ON racing_analysis_history(updated_at);
CREATE INDEX idx_racing_analysis_confirmed ON racing_add_intents(confirmed_at,candidate_key);
