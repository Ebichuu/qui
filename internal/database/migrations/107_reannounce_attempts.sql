-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later
CREATE TABLE reannounce_attempts (
 instance_id INTEGER NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
 torrent_hash TEXT NOT NULL,
 started_ns BIGINT NOT NULL,
 not_before_ns BIGINT NOT NULL,
 PRIMARY KEY(instance_id,torrent_hash)
);
