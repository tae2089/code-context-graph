CREATE TABLE IF NOT EXISTS parse_cache_entries (
    id bigserial PRIMARY KEY,
    namespace varchar(256) NOT NULL DEFAULT 'default',
    file_path varchar(768) NOT NULL,
    source_hash varchar(64) NOT NULL,
    parser_version varchar(160) NOT NULL,
    context_hash varchar(64) NOT NULL,
    payload bytea NOT NULL,
    created_at timestamptz,
    updated_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_parse_cache_ns_file
ON parse_cache_entries(namespace, file_path);

CREATE INDEX IF NOT EXISTS idx_parse_cache_source_hash
ON parse_cache_entries(source_hash);
