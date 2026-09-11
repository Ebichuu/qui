-- Copyright (c) 2026, s0up and the autobrr contributors.
-- SPDX-License-Identifier: GPL-2.0-or-later

CREATE TABLE automatic_delete_intents (
    instance_id INTEGER NOT NULL,
    torrent_hash TEXT NOT NULL,
    added_on BIGINT NOT NULL,
    operation_id TEXT NOT NULL,
    owner TEXT NOT NULL,
    action TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('submitted','accepted','unknown','confirmed')),
    submitted_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(instance_id,torrent_hash,added_on)
);
CREATE INDEX idx_automatic_delete_operation ON automatic_delete_intents(operation_id);
