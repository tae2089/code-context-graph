CREATE TABLE IF NOT EXISTS parse_cache_entries (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    file_path text NOT NULL,
    source_hash text NOT NULL,
    parser_version text NOT NULL,
    context_hash text NOT NULL,
    payload blob NOT NULL,
    created_at datetime,
    updated_at datetime
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_parse_cache_ns_file
ON parse_cache_entries(namespace, file_path);

CREATE INDEX IF NOT EXISTS idx_parse_cache_source_hash
ON parse_cache_entries(source_hash);
