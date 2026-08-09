-- The intent index carries no `language` column on purpose. Language is a fact
-- about the file, and indexing it here would let a query word match something
-- other than the recorded reason, which is exactly what this table prevents.
CREATE VIRTUAL TABLE IF NOT EXISTS intent_fts
USING fts5(node_id UNINDEXED, content, namespace UNINDEXED);
