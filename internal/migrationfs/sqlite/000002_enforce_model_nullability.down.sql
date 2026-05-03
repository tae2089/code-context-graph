DROP INDEX IF EXISTS idx_ns_qn_fp_sl;
DROP INDEX IF EXISTS idx_nodes_kind;
DROP INDEX IF EXISTS idx_nodes_file_path;
DROP INDEX IF EXISTS idx_nodes_language;
CREATE TABLE nodes_new (
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
INSERT INTO nodes_new(id, namespace, qualified_name, kind, name, file_path, start_line, end_line, hash, language, created_at, updated_at)
SELECT id, namespace, qualified_name, kind, name, file_path, start_line, end_line, hash, language, created_at, updated_at
FROM nodes;
DROP TABLE nodes;
ALTER TABLE nodes_new RENAME TO nodes;
CREATE UNIQUE INDEX IF NOT EXISTS idx_ns_qn_fp_sl ON nodes(namespace, qualified_name, file_path, start_line);
CREATE INDEX IF NOT EXISTS idx_nodes_kind ON nodes(kind);
CREATE INDEX IF NOT EXISTS idx_nodes_file_path ON nodes(file_path);
CREATE INDEX IF NOT EXISTS idx_nodes_language ON nodes(language);

DROP INDEX IF EXISTS idx_edges_namespace_fingerprint;
DROP INDEX IF EXISTS idx_edges_namespace;
DROP INDEX IF EXISTS idx_edges_from_node_id;
DROP INDEX IF EXISTS idx_edges_to_node_id;
DROP INDEX IF EXISTS idx_edges_kind;
DROP INDEX IF EXISTS idx_edges_file_path;
CREATE TABLE edges_new (
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
INSERT INTO edges_new(id, namespace, from_node_id, to_node_id, kind, file_path, line, fingerprint, created_at)
SELECT id, namespace, from_node_id, to_node_id, kind, file_path, line, fingerprint, created_at
FROM edges;
DROP TABLE edges;
ALTER TABLE edges_new RENAME TO edges;
CREATE UNIQUE INDEX IF NOT EXISTS idx_edges_namespace_fingerprint ON edges(namespace, fingerprint);
CREATE INDEX IF NOT EXISTS idx_edges_namespace ON edges(namespace);
CREATE INDEX IF NOT EXISTS idx_edges_from_node_id ON edges(from_node_id);
CREATE INDEX IF NOT EXISTS idx_edges_to_node_id ON edges(to_node_id);
CREATE INDEX IF NOT EXISTS idx_edges_kind ON edges(kind);
CREATE INDEX IF NOT EXISTS idx_edges_file_path ON edges(file_path);

DROP INDEX IF EXISTS idx_community_ns_key;
DROP INDEX IF EXISTS idx_communities_strategy;
CREATE TABLE communities_new (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    key text,
    label text NOT NULL,
    strategy text NOT NULL,
    description text,
    created_at datetime,
    updated_at datetime
);
INSERT INTO communities_new(id, namespace, key, label, strategy, description, created_at, updated_at)
SELECT id, namespace, key, label, strategy, description, created_at, updated_at
FROM communities;
DROP TABLE communities;
ALTER TABLE communities_new RENAME TO communities;
CREATE UNIQUE INDEX IF NOT EXISTS idx_community_ns_key ON communities(namespace, key);
CREATE INDEX IF NOT EXISTS idx_communities_strategy ON communities(strategy);

DROP INDEX IF EXISTS idx_community_node;
DROP INDEX IF EXISTS idx_community_memberships_node_id;
CREATE TABLE community_memberships_new (
    id integer PRIMARY KEY AUTOINCREMENT,
    community_id integer,
    node_id integer,
    created_at datetime
);
INSERT INTO community_memberships_new(id, community_id, node_id, created_at)
SELECT id, community_id, node_id, created_at
FROM community_memberships;
DROP TABLE community_memberships;
ALTER TABLE community_memberships_new RENAME TO community_memberships;
CREATE UNIQUE INDEX IF NOT EXISTS idx_community_node ON community_memberships(community_id, node_id);
CREATE INDEX IF NOT EXISTS idx_community_memberships_node_id ON community_memberships(node_id);

DROP INDEX IF EXISTS idx_flows_namespace;
CREATE TABLE flows_new (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    name text,
    description text,
    created_at datetime
);
INSERT INTO flows_new(id, namespace, name, description, created_at)
SELECT id, namespace, name, description, created_at
FROM flows;
DROP TABLE flows;
ALTER TABLE flows_new RENAME TO flows;
CREATE INDEX IF NOT EXISTS idx_flows_namespace ON flows(namespace);

DROP INDEX IF EXISTS idx_flow_memberships_namespace;
DROP INDEX IF EXISTS idx_flow_memberships_flow_id;
DROP INDEX IF EXISTS idx_flow_memberships_node_id;
CREATE TABLE flow_memberships_new (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    flow_id integer,
    node_id integer,
    ordinal integer NOT NULL
);
INSERT INTO flow_memberships_new(id, namespace, flow_id, node_id, ordinal)
SELECT id, namespace, flow_id, node_id, ordinal
FROM flow_memberships;
DROP TABLE flow_memberships;
ALTER TABLE flow_memberships_new RENAME TO flow_memberships;
CREATE INDEX IF NOT EXISTS idx_flow_memberships_namespace ON flow_memberships(namespace);
CREATE INDEX IF NOT EXISTS idx_flow_memberships_flow_id ON flow_memberships(flow_id);
CREATE INDEX IF NOT EXISTS idx_flow_memberships_node_id ON flow_memberships(node_id);
