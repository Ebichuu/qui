-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later

ALTER TABLE racing_sources ADD COLUMN adapter TEXT NOT NULL DEFAULT '';
ALTER TABLE racing_sources ADD COLUMN page_count INTEGER NOT NULL DEFAULT 1 CHECK (page_count BETWEEN 1 AND 5);
ALTER TABLE racing_sources ADD COLUMN initial_lookback_seconds INTEGER NOT NULL DEFAULT 0 CHECK (initial_lookback_seconds BETWEEN 0 AND 604800);
UPDATE racing_sources SET adapter='generic-rss' WHERE kind='rss';

CREATE TABLE racing_source_cursors (
    source_id INTEGER PRIMARY KEY REFERENCES racing_sources(id) ON DELETE CASCADE,
    baseline_at TEXT NOT NULL,
    last_success_at TEXT
);
CREATE TABLE racing_discoveries (
    id BIGSERIAL PRIMARY KEY,
    source_id INTEGER NOT NULL REFERENCES racing_sources(id) ON DELETE CASCADE,
    site_id INTEGER NOT NULL,
    event_key TEXT NOT NULL,
    public_item TEXT NOT NULL,
    private_ciphertext TEXT NOT NULL,
    eligible INTEGER NOT NULL CHECK (eligible IN (0, 1)),
    first_seen_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    UNIQUE (source_id, site_id, event_key)
);
CREATE INDEX idx_racing_discoveries_seen ON racing_discoveries(last_seen_at);
