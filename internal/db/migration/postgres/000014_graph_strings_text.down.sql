ALTER TABLE unresolved_index_states
    ALTER COLUMN namespace TYPE varchar(256),
    ALTER COLUMN version TYPE varchar(64);

ALTER TABLE unresolved_edge_candidates
    ALTER COLUMN namespace TYPE varchar(256),
    ALTER COLUMN lookup_key_hash TYPE varchar(64),
    ALTER COLUMN fingerprint_hash TYPE varchar(64),
    ALTER COLUMN file_path TYPE varchar(1024),
    ALTER COLUMN kind TYPE varchar(32);

ALTER TABLE parse_cache_entries
    ALTER COLUMN namespace TYPE varchar(256),
    ALTER COLUMN file_path TYPE varchar(768),
    ALTER COLUMN source_hash TYPE varchar(64),
    ALTER COLUMN parser_version TYPE varchar(160),
    ALTER COLUMN context_hash TYPE varchar(64);

ALTER TABLE search_documents
    ALTER COLUMN namespace TYPE varchar(256),
    ALTER COLUMN language TYPE varchar(32);

ALTER TABLE flow_memberships
    ALTER COLUMN namespace TYPE varchar(256);

ALTER TABLE flows
    ALTER COLUMN namespace TYPE varchar(256),
    ALTER COLUMN name TYPE varchar(256);

ALTER TABLE communities
    ALTER COLUMN namespace TYPE varchar(256),
    ALTER COLUMN key TYPE varchar(512),
    ALTER COLUMN label TYPE varchar(256),
    ALTER COLUMN strategy TYPE varchar(32);

ALTER TABLE doc_tags
    ALTER COLUMN kind TYPE varchar(32),
    ALTER COLUMN name TYPE varchar(128);

ALTER TABLE edges
    ALTER COLUMN namespace TYPE varchar(256),
    ALTER COLUMN kind TYPE varchar(32),
    ALTER COLUMN file_path TYPE varchar(1024);

ALTER TABLE nodes
    ALTER COLUMN namespace TYPE varchar(256),
    ALTER COLUMN qualified_name TYPE varchar(512),
    ALTER COLUMN kind TYPE varchar(32),
    ALTER COLUMN name TYPE varchar(256),
    ALTER COLUMN file_path TYPE varchar(768),
    ALTER COLUMN hash TYPE varchar(64),
    ALTER COLUMN language TYPE varchar(32);
