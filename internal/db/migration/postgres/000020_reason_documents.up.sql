-- One row per recorded reason, replacing the one joined intent_content column
-- per node. Scoring reads a row as a document, and a joined document charged a
-- node's @intent for the length of every @domainRule written beside it.
CREATE TABLE IF NOT EXISTS search_reasons (
    id bigserial PRIMARY KEY,
    namespace text NOT NULL DEFAULT 'default',
    node_id bigint NOT NULL,
    content text NOT NULL,
    reason_tsv tsvector
);

CREATE INDEX IF NOT EXISTS idx_searchreason_ns_node ON search_reasons(namespace, node_id);

-- The same separator translation the name and intent vectors already use:
-- FTS5's unicode61 tokenizer splits on '/', '.' and '_', and this vector has to
-- see the same tokens or the two backends answer the same question differently.
CREATE OR REPLACE FUNCTION search_reasons_tsv_trigger() RETURNS trigger AS $$
BEGIN
    NEW.reason_tsv := to_tsvector('simple', translate(COALESCE(NEW.content, ''), '/._', '   '));
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_search_reasons_tsv ON search_reasons;
CREATE TRIGGER trg_search_reasons_tsv
BEFORE INSERT OR UPDATE ON search_reasons
FOR EACH ROW EXECUTE FUNCTION search_reasons_tsv_trigger();

CREATE INDEX IF NOT EXISTS idx_search_reasons_tsv ON search_reasons USING gin(reason_tsv);

-- Refill from the annotations themselves rather than from intent_content, whose
-- joined text cannot be split back into the tags it came from. Only the tags
-- indexed today are taken: every @domainRule, and the first @intent, because a
-- declaration states one purpose and only the first one is ever displayed.
INSERT INTO search_reasons (namespace, node_id, content)
SELECT n.namespace, n.id, btrim(t.value)
FROM nodes n
JOIN annotations a ON a.node_id = n.id
JOIN doc_tags t ON t.annotation_id = a.id
WHERE n.kind IN ('function', 'class', 'type', 'test', 'file')
  AND btrim(t.value) <> ''
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

DROP INDEX IF EXISTS idx_search_documents_intent_tsv;
DROP TRIGGER IF EXISTS trg_search_documents_intent_tsv ON search_documents;
DROP FUNCTION IF EXISTS search_documents_intent_tsv_trigger();
ALTER TABLE search_documents DROP COLUMN IF EXISTS intent_tsv;
ALTER TABLE search_documents DROP COLUMN IF EXISTS intent_content;
