ALTER TABLE search_documents ADD COLUMN intent_content text NOT NULL DEFAULT '';

-- Rejoin every reason of a node into the one document the old index held. The
-- ORDER BY inside group_concat needs SQLite 3.44 or newer, which is what the
-- driver bundles; on an older library this fails loudly rather than silently
-- joining the reasons in an order nobody chose.
UPDATE search_documents
SET intent_content = COALESCE((
    SELECT group_concat(r.content, ' ' ORDER BY r.id)
    FROM search_reasons r
    WHERE r.namespace = search_documents.namespace AND r.node_id = search_documents.node_id
), '');

DELETE FROM intent_fts;

INSERT INTO intent_fts(node_id, content, namespace)
SELECT node_id, intent_content, namespace FROM search_documents WHERE intent_content <> '';

DROP INDEX IF EXISTS idx_searchreason_ns_node;

DROP TABLE IF EXISTS search_reasons;
