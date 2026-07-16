ALTER TABLE nodes
    ALTER COLUMN namespace TYPE text,
    ALTER COLUMN qualified_name TYPE text,
    ALTER COLUMN kind TYPE text,
    ALTER COLUMN name TYPE text,
    ALTER COLUMN file_path TYPE text,
    ALTER COLUMN hash TYPE text,
    ALTER COLUMN language TYPE text;

ALTER TABLE edges
    ALTER COLUMN namespace TYPE text,
    ALTER COLUMN kind TYPE text,
    ALTER COLUMN file_path TYPE text;

ALTER TABLE doc_tags
    ALTER COLUMN kind TYPE text,
    ALTER COLUMN name TYPE text;

ALTER TABLE communities
    ALTER COLUMN namespace TYPE text,
    ALTER COLUMN key TYPE text,
    ALTER COLUMN label TYPE text,
    ALTER COLUMN strategy TYPE text;

ALTER TABLE flows
    ALTER COLUMN namespace TYPE text,
    ALTER COLUMN name TYPE text;

ALTER TABLE flow_memberships
    ALTER COLUMN namespace TYPE text;

ALTER TABLE search_documents
    ALTER COLUMN namespace TYPE text,
    ALTER COLUMN language TYPE text;

ALTER TABLE parse_cache_entries
    ALTER COLUMN namespace TYPE text,
    ALTER COLUMN file_path TYPE text,
    ALTER COLUMN source_hash TYPE text,
    ALTER COLUMN parser_version TYPE text,
    ALTER COLUMN context_hash TYPE text;

ALTER TABLE unresolved_edge_candidates
    ALTER COLUMN namespace TYPE text,
    ALTER COLUMN lookup_key_hash TYPE text,
    ALTER COLUMN fingerprint_hash TYPE text,
    ALTER COLUMN file_path TYPE text,
    ALTER COLUMN kind TYPE text;

ALTER TABLE unresolved_index_states
    ALTER COLUMN namespace TYPE text,
    ALTER COLUMN version TYPE text;
