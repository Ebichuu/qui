-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later

-- Q1 configuration only: no automatic operation intents or execution ownership.
CREATE TABLE racing_secrets (
    id BIGSERIAL PRIMARY KEY,
    ciphertext TEXT NOT NULL
);

CREATE TABLE racing_sites (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    enabled BIGINT NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    tracker_hosts TEXT NOT NULL DEFAULT '[]',
    request_interval_seconds BIGINT NOT NULL CHECK (request_interval_seconds > 0),
    credential_id BIGINT REFERENCES racing_secrets(id) ON DELETE RESTRICT,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_racing_sites_credential ON racing_sites(credential_id);

CREATE TABLE racing_sources (
    id BIGSERIAL PRIMARY KEY,
    site_id BIGINT NOT NULL REFERENCES racing_sites(id) ON DELETE RESTRICT,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('rss', 'web', 'revival')),
    enabled BIGINT NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    interval_seconds BIGINT NOT NULL CHECK (interval_seconds > 0),
    endpoint_id BIGINT NOT NULL REFERENCES racing_secrets(id) ON DELETE RESTRICT,
    url_origin TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_racing_sources_site ON racing_sources(site_id);
CREATE INDEX idx_racing_sources_endpoint ON racing_sources(endpoint_id);

CREATE TABLE racing_groups (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    enabled BIGINT NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    updated_at TEXT NOT NULL
);
CREATE TABLE racing_group_members (
    group_id BIGINT NOT NULL REFERENCES racing_groups(id) ON DELETE CASCADE,
    instance_id BIGINT NOT NULL REFERENCES instances(id) ON DELETE RESTRICT,
    PRIMARY KEY (group_id, instance_id)
);
CREATE INDEX idx_racing_group_members_instance ON racing_group_members(instance_id);

CREATE TABLE racing_rules (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    enabled BIGINT NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    sort_order BIGINT NOT NULL DEFAULT 0,
    accept_kinds TEXT NOT NULL,
    filters TEXT NOT NULL,
    receive_window_seconds BIGINT NOT NULL CHECK (receive_window_seconds > 0),
    target_group_id BIGINT REFERENCES racing_groups(id) ON DELETE RESTRICT,
    target_instance_id BIGINT REFERENCES instances(id) ON DELETE RESTRICT,
    allow_official_reclaim BIGINT NOT NULL DEFAULT 0 CHECK (allow_official_reclaim IN (0, 1)),
    updated_at TEXT NOT NULL,
    CHECK ((target_group_id IS NOT NULL AND target_instance_id IS NULL) OR
           (target_group_id IS NULL AND target_instance_id IS NOT NULL))
);
CREATE INDEX idx_racing_rules_group ON racing_rules(target_group_id);
CREATE INDEX idx_racing_rules_instance ON racing_rules(target_instance_id);
CREATE INDEX idx_racing_rules_order ON racing_rules(sort_order, id);
CREATE TABLE racing_rule_sources (
    rule_id BIGINT NOT NULL REFERENCES racing_rules(id) ON DELETE CASCADE,
    source_id BIGINT NOT NULL REFERENCES racing_sources(id) ON DELETE RESTRICT,
    PRIMARY KEY (rule_id, source_id)
);
CREATE INDEX idx_racing_rule_sources_source ON racing_rule_sources(source_id);
