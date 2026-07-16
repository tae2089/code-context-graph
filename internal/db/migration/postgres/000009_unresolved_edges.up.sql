CREATE TABLE IF NOT EXISTS unresolved_edge_candidates (
    id bigserial PRIMARY KEY,
    namespace varchar(256) NOT NULL DEFAULT 'default',
    lookup_key varchar(512) NOT NULL,
    fingerprint text NOT NULL,
    file_path varchar(1024) NOT NULL,
    kind varchar(32) NOT NULL,
    line integer,
    created_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_unresolved_ns_fp
ON unresolved_edge_candidates(namespace, fingerprint);
CREATE INDEX IF NOT EXISTS idx_unresolved_edge_candidates_lookup_key
ON unresolved_edge_candidates(lookup_key);
CREATE INDEX IF NOT EXISTS idx_unresolved_edge_candidates_file_path
ON unresolved_edge_candidates(file_path);

CREATE TABLE IF NOT EXISTS unresolved_index_states (
    namespace varchar(256) NOT NULL PRIMARY KEY,
    updated_at timestamptz
);
