-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later

ALTER TABLE dashboard_settings
    ADD COLUMN server_stats_sort_column TEXT NOT NULL DEFAULT 'instance';

ALTER TABLE dashboard_settings
    ADD COLUMN server_stats_sort_direction TEXT NOT NULL DEFAULT 'asc';
