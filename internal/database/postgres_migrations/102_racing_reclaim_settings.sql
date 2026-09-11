-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later

CREATE TABLE racing_reclaim_settings (
    id SERIAL PRIMARY KEY,
    instance_id INTEGER UNIQUE REFERENCES instances(id) ON DELETE CASCADE,
    group_id INTEGER UNIQUE REFERENCES racing_groups(id) ON DELETE CASCADE,
    policy TEXT NOT NULL,
    CHECK ((instance_id IS NOT NULL AND group_id IS NULL) OR (instance_id IS NULL AND group_id IS NOT NULL))
);
CREATE TABLE racing_reclaim_rule_refs (
    setting_id INTEGER NOT NULL REFERENCES racing_reclaim_settings(id) ON DELETE CASCADE,
    rule_id INTEGER NOT NULL REFERENCES automations(id) ON DELETE RESTRICT,
    PRIMARY KEY(setting_id,rule_id)
);
CREATE INDEX idx_racing_reclaim_rule_refs_rule ON racing_reclaim_rule_refs(rule_id);
