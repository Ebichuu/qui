-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later
CREATE TABLE reclaim_upload_samples (
 instance_id INTEGER NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
 torrent_hash TEXT NOT NULL,
 added_on BIGINT NOT NULL,
 observed_ns BIGINT NOT NULL,
 uploaded BIGINT NOT NULL CHECK(uploaded >= 0),
 PRIMARY KEY(instance_id,torrent_hash,added_on,observed_ns)
);
CREATE INDEX idx_reclaim_upload_expiry ON reclaim_upload_samples(instance_id,observed_ns);
