CREATE INDEX IF NOT EXISTS idx_nodes_ns_file_path
    ON nodes(namespace, file_path);
