-- No-op for SQLite. FTS5's unicode61 tokenizer already splits on '/', '.',
-- and '_'; the matching PostgreSQL migration teaches its tsvector triggers the
-- same splits. This file exists so both drivers reach the same schema version,
-- which RequiredSchemaVersion pins.
SELECT 1;
