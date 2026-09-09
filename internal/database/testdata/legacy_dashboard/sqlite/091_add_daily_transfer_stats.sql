-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later

CREATE TABLE IF NOT EXISTS instance_daily_transfer_stats (
    instance_id INTEGER NOT NULL,
    day TEXT NOT NULL,
    downloaded INTEGER NOT NULL DEFAULT 0,
    uploaded INTEGER NOT NULL DEFAULT 0,
    last_alltime_dl INTEGER NOT NULL DEFAULT 0,
    last_alltime_ul INTEGER NOT NULL DEFAULT 0,
    last_sample_at TEXT NOT NULL,
    PRIMARY KEY (instance_id, day),
    FOREIGN KEY (instance_id) REFERENCES instances(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_daily_transfer_stats_day
    ON instance_daily_transfer_stats(day);
