CREATE TABLE racing_storage_pools (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE racing_path_mappings (
    id BIGSERIAL PRIMARY KEY,
    instance_id BIGINT NOT NULL REFERENCES instances(id) ON DELETE RESTRICT,
    storage_pool_id BIGINT NOT NULL REFERENCES racing_storage_pools(id) ON DELETE RESTRICT,
    path TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (instance_id, path)
);
CREATE INDEX idx_racing_path_mappings_pool ON racing_path_mappings(storage_pool_id);
