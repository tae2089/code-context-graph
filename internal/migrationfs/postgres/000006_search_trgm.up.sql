-- pg_trgm powers typo-tolerant fuzzy symbol search. The extension may require elevated
-- privileges, so attempt it inside a DO block and degrade gracefully when it is unavailable:
-- the trigram indexes are only created when the extension is present.
DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_trgm;
EXCEPTION WHEN insufficient_privilege THEN
    RAISE WARNING 'pg_trgm unavailable; fuzzy symbol search disabled';
END
$$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        CREATE INDEX IF NOT EXISTS idx_nodes_name_trgm ON nodes USING gin (name gin_trgm_ops);
        CREATE INDEX IF NOT EXISTS idx_nodes_qualified_name_trgm ON nodes USING gin (qualified_name gin_trgm_ops);
    END IF;
END
$$;
