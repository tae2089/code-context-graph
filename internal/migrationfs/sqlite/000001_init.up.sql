CREATE TABLE IF NOT EXISTS nodes (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    qualified_name text,
    kind text,
    name text,
    file_path text,
    start_line integer,
    end_line integer,
    hash text,
    language text,
    created_at datetime,
    updated_at datetime
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ns_qn_fp_sl ON nodes(namespace, qualified_name, file_path, start_line);
CREATE INDEX IF NOT EXISTS idx_nodes_kind ON nodes(kind);
CREATE INDEX IF NOT EXISTS idx_nodes_file_path ON nodes(file_path);
CREATE INDEX IF NOT EXISTS idx_nodes_language ON nodes(language);

CREATE TABLE IF NOT EXISTS edges (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    from_node_id integer,
    to_node_id integer,
    kind text,
    file_path text,
    line integer,
    fingerprint text,
    created_at datetime
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_edges_namespace_fingerprint ON edges(namespace, fingerprint);
CREATE INDEX IF NOT EXISTS idx_edges_namespace ON edges(namespace);
CREATE INDEX IF NOT EXISTS idx_edges_from_node_id ON edges(from_node_id);
CREATE INDEX IF NOT EXISTS idx_edges_to_node_id ON edges(to_node_id);
CREATE INDEX IF NOT EXISTS idx_edges_kind ON edges(kind);
CREATE INDEX IF NOT EXISTS idx_edges_file_path ON edges(file_path);

CREATE TABLE IF NOT EXISTS annotations (
    id integer PRIMARY KEY AUTOINCREMENT,
    node_id integer NOT NULL,
    summary text,
    context text,
    raw_text text,
    created_at datetime,
    updated_at datetime
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_annotations_node_id ON annotations(node_id);

CREATE TABLE IF NOT EXISTS doc_tags (
    id integer PRIMARY KEY AUTOINCREMENT,
    annotation_id integer NOT NULL,
    kind text NOT NULL,
    type text,
    name text,
    value text NOT NULL,
    ordinal integer NOT NULL,
    created_at datetime
);

CREATE INDEX IF NOT EXISTS idx_doc_tags_annotation_id ON doc_tags(annotation_id);
CREATE INDEX IF NOT EXISTS idx_doc_tags_kind ON doc_tags(kind);

CREATE TABLE IF NOT EXISTS communities (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    key text,
    label text NOT NULL,
    strategy text NOT NULL,
    description text,
    created_at datetime,
    updated_at datetime
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_community_ns_key ON communities(namespace, key);
CREATE INDEX IF NOT EXISTS idx_communities_strategy ON communities(strategy);

CREATE TABLE IF NOT EXISTS community_memberships (
    id integer PRIMARY KEY AUTOINCREMENT,
    community_id integer,
    node_id integer,
    created_at datetime
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_community_node ON community_memberships(community_id, node_id);
CREATE INDEX IF NOT EXISTS idx_community_memberships_node_id ON community_memberships(node_id);

CREATE TABLE IF NOT EXISTS flows (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    name text,
    description text,
    created_at datetime
);

CREATE INDEX IF NOT EXISTS idx_flows_namespace ON flows(namespace);

CREATE TABLE IF NOT EXISTS flow_memberships (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    flow_id integer,
    node_id integer,
    ordinal integer NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_flow_memberships_namespace ON flow_memberships(namespace);
CREATE INDEX IF NOT EXISTS idx_flow_memberships_flow_id ON flow_memberships(flow_id);
CREATE INDEX IF NOT EXISTS idx_flow_memberships_node_id ON flow_memberships(node_id);

CREATE TABLE IF NOT EXISTS search_documents (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    node_id integer NOT NULL,
    content text NOT NULL,
    language text
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_searchdoc_ns_node ON search_documents(namespace, node_id);
CREATE INDEX IF NOT EXISTS idx_search_documents_namespace ON search_documents(namespace);
CREATE INDEX IF NOT EXISTS idx_search_documents_language ON search_documents(language);

CREATE VIRTUAL TABLE IF NOT EXISTS search_fts
USING fts5(node_id UNINDEXED, content, language, namespace UNINDEXED);
