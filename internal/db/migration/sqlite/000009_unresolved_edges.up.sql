CREATE TABLE IF NOT EXISTS unresolved_edge_candidates (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    lookup_key text NOT NULL,
    fingerprint text NOT NULL,
    file_path text NOT NULL,
    kind text NOT NULL,
    line integer,
    created_at datetime
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_unresolved_ns_fp
ON unresolved_edge_candidates(namespace, fingerprint);
CREATE INDEX IF NOT EXISTS idx_unresolved_edge_candidates_lookup_key
ON unresolved_edge_candidates(lookup_key);
CREATE INDEX IF NOT EXISTS idx_unresolved_edge_candidates_file_path
ON unresolved_edge_candidates(file_path);

CREATE TABLE IF NOT EXISTS unresolved_index_states (
    namespace text NOT NULL PRIMARY KEY,
    updated_at datetime
);
