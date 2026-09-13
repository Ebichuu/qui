-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later
ALTER TABLE racing_instance_policies ADD COLUMN reclaim_enabled INTEGER NOT NULL DEFAULT 0 CHECK(reclaim_enabled IN (0,1));
