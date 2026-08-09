-- No-op for PostgreSQL. The matching SQLite migration creates the intent_fts
-- virtual table; PostgreSQL already got its intent index in 000017 as the
-- intent_tsv column, its trigger, and a GIN index. This file exists so both
-- drivers reach the same schema version, which RequiredSchemaVersion pins.
SELECT 1;
