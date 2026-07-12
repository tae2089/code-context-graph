CREATE TABLE IF NOT EXISTS nodes (
    id bigserial PRIMARY KEY,
    namespace varchar(256) NOT NULL DEFAULT 'default',
    qualified_name varchar(512),
    kind varchar(32),
    name varchar(256),
    file_path varchar(768),
    start_line bigint,
    end_line bigint,
    hash varchar(64),
    language varchar(32),
    created_at timestamptz,
    updated_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ns_qn_fp_sl ON nodes(namespace, qualified_name, file_path, start_line);
CREATE INDEX IF NOT EXISTS idx_nodes_kind ON nodes(kind);
CREATE INDEX IF NOT EXISTS idx_nodes_file_path ON nodes(file_path);
CREATE INDEX IF NOT EXISTS idx_nodes_language ON nodes(language);

CREATE TABLE IF NOT EXISTS edges (
    id bigserial PRIMARY KEY,
    namespace varchar(256) NOT NULL DEFAULT 'default',
    from_node_id bigint,
    to_node_id bigint,
    kind varchar(32),
    file_path varchar(1024),
    line bigint,
    fingerprint text,
    created_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_edges_namespace_fingerprint ON edges(namespace, fingerprint);
CREATE INDEX IF NOT EXISTS idx_edges_namespace ON edges(namespace);
CREATE INDEX IF NOT EXISTS idx_edges_from_node_id ON edges(from_node_id);
CREATE INDEX IF NOT EXISTS idx_edges_to_node_id ON edges(to_node_id);
CREATE INDEX IF NOT EXISTS idx_edges_kind ON edges(kind);
CREATE INDEX IF NOT EXISTS idx_edges_file_path ON edges(file_path);

CREATE TABLE IF NOT EXISTS annotations (
    id bigserial PRIMARY KEY,
    node_id bigint NOT NULL,
    summary varchar(1024),
    context varchar(2048),
    raw_text text,
    created_at timestamptz,
    updated_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_annotations_node_id ON annotations(node_id);

CREATE TABLE IF NOT EXISTS doc_tags (
    id bigserial PRIMARY KEY,
    annotation_id bigint NOT NULL,
    kind varchar(32) NOT NULL,
    type text,
    name varchar(128),
    value text NOT NULL,
    ordinal bigint NOT NULL,
    created_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_doc_tags_annotation_id ON doc_tags(annotation_id);
CREATE INDEX IF NOT EXISTS idx_doc_tags_kind ON doc_tags(kind);

CREATE TABLE IF NOT EXISTS communities (
    id bigserial PRIMARY KEY,
    namespace varchar(256) NOT NULL DEFAULT 'default',
    key varchar(512),
    label varchar(256) NOT NULL,
    strategy varchar(32) NOT NULL,
    description text,
    created_at timestamptz,
    updated_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_community_ns_key ON communities(namespace, key);
CREATE INDEX IF NOT EXISTS idx_communities_strategy ON communities(strategy);

CREATE TABLE IF NOT EXISTS community_memberships (
    id bigserial PRIMARY KEY,
    community_id bigint,
    node_id bigint,
    created_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_community_node ON community_memberships(community_id, node_id);
CREATE INDEX IF NOT EXISTS idx_community_memberships_node_id ON community_memberships(node_id);

CREATE TABLE IF NOT EXISTS flows (
    id bigserial PRIMARY KEY,
    namespace varchar(256) NOT NULL DEFAULT 'default',
    name varchar(256),
    description text,
    created_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_flows_namespace ON flows(namespace);

CREATE TABLE IF NOT EXISTS flow_memberships (
    id bigserial PRIMARY KEY,
    namespace varchar(256) NOT NULL DEFAULT 'default',
    flow_id bigint,
    node_id bigint,
    ordinal bigint NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_flow_memberships_namespace ON flow_memberships(namespace);
CREATE INDEX IF NOT EXISTS idx_flow_memberships_flow_id ON flow_memberships(flow_id);
CREATE INDEX IF NOT EXISTS idx_flow_memberships_node_id ON flow_memberships(node_id);

CREATE TABLE IF NOT EXISTS search_documents (
    id bigserial PRIMARY KEY,
    namespace varchar(256) NOT NULL DEFAULT 'default',
    node_id bigint NOT NULL,
    content text NOT NULL,
    language varchar(32),
    tsv tsvector
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_searchdoc_ns_node ON search_documents(namespace, node_id);
CREATE INDEX IF NOT EXISTS idx_search_documents_namespace ON search_documents(namespace);
CREATE INDEX IF NOT EXISTS idx_search_documents_language ON search_documents(language);

CREATE OR REPLACE FUNCTION search_documents_tsv_trigger() RETURNS trigger AS $$
BEGIN
    NEW.tsv := to_tsvector('simple', COALESCE(NEW.content, ''));
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_search_documents_tsv ON search_documents;
CREATE TRIGGER trg_search_documents_tsv
BEFORE INSERT OR UPDATE ON search_documents
FOR EACH ROW EXECUTE FUNCTION search_documents_tsv_trigger();

CREATE INDEX IF NOT EXISTS idx_search_documents_tsv
ON search_documents USING gin(tsv);
