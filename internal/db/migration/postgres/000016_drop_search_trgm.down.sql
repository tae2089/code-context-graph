DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        CREATE INDEX IF NOT EXISTS idx_nodes_name_trgm ON nodes USING gin (name gin_trgm_ops);
        CREATE INDEX IF NOT EXISTS idx_nodes_qualified_name_trgm ON nodes USING gin (qualified_name gin_trgm_ops);
    END IF;
END
$$;
