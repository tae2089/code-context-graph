-- One row per recorded reason, replacing the one joined intent_content column
-- per node. Scoring reads a row as a document, and a joined document charged a
-- node's @intent for the length of every @domainRule written beside it.
CREATE TABLE IF NOT EXISTS search_reasons (
    id integer PRIMARY KEY AUTOINCREMENT,
    namespace text NOT NULL DEFAULT 'default',
    node_id integer NOT NULL,
    content text NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_searchreason_ns_node ON search_reasons(namespace, node_id);

-- Refill from the annotations themselves rather than from intent_content, whose
-- joined text cannot be split back into the tags it came from. Only the tags
-- indexed today are taken: every @domainRule, and the first @intent, because a
-- declaration states one purpose and only the first one is ever displayed.
INSERT INTO search_reasons (namespace, node_id, content)
SELECT n.namespace, n.id, trim(t.value)
FROM nodes n
JOIN annotations a ON a.node_id = n.id
JOIN doc_tags t ON t.annotation_id = a.id
WHERE n.kind IN ('function', 'class', 'type', 'test', 'file')
  AND trim(t.value) <> ''
  AND (
    t.kind = 'domainRule'
    OR t.id = (
        SELECT t2.id FROM doc_tags t2
        WHERE t2.annotation_id = a.id AND t2.kind = 'intent'
        ORDER BY t2.ordinal, t2.id
        LIMIT 1
    )
  )
ORDER BY n.id, t.ordinal, t.id;

-- intent_fts keeps its shape; what changes is that a node now owns one row per
-- reason instead of one row. Reloading it here means an existing database is
-- searchable under the new index without waiting for a graph rebuild.
DELETE FROM intent_fts;

INSERT INTO intent_fts(node_id, content, namespace)
SELECT node_id, content, namespace FROM search_reasons ORDER BY id;

ALTER TABLE search_documents DROP COLUMN intent_content;
