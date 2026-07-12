CREATE INDEX IF NOT EXISTS idx_edges_ns_from_kind_to
ON edges (namespace, from_node_id, kind, to_node_id);

CREATE INDEX IF NOT EXISTS idx_edges_ns_to_kind_from
ON edges (namespace, to_node_id, kind, from_node_id);
