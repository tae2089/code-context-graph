CREATE TABLE IF NOT EXISTS cross_refs (
    id integer PRIMARY KEY AUTOINCREMENT,
    from_namespace text NOT NULL,
    from_node_id integer NOT NULL,
    raw text NOT NULL,
    to_namespace text NOT NULL,
    to_path text NOT NULL DEFAULT '',
    to_symbol text NOT NULL DEFAULT '',
    resolved_node_id integer,
    status text NOT NULL,
    source text NOT NULL DEFAULT 'annotation',
    created_at datetime,
    updated_at datetime
);

CREATE INDEX IF NOT EXISTS idx_crossref_from_ns ON cross_refs(from_namespace);
CREATE INDEX IF NOT EXISTS idx_crossref_from_node ON cross_refs(from_node_id);
CREATE INDEX IF NOT EXISTS idx_crossref_to_ns ON cross_refs(to_namespace);
CREATE INDEX IF NOT EXISTS idx_crossref_resolved_node ON cross_refs(resolved_node_id);
