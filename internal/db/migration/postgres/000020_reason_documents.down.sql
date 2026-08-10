ALTER TABLE search_documents ADD COLUMN IF NOT EXISTS intent_content text NOT NULL DEFAULT '';
ALTER TABLE search_documents ADD COLUMN IF NOT EXISTS intent_tsv tsvector;

CREATE OR REPLACE FUNCTION search_documents_intent_tsv_trigger() RETURNS trigger AS $$
BEGIN
    NEW.intent_tsv := to_tsvector('simple', translate(COALESCE(NEW.intent_content, ''), '/._', '   '));
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_search_documents_intent_tsv ON search_documents;
CREATE TRIGGER trg_search_documents_intent_tsv
BEFORE INSERT OR UPDATE ON search_documents
FOR EACH ROW EXECUTE FUNCTION search_documents_intent_tsv_trigger();

CREATE INDEX IF NOT EXISTS idx_search_documents_intent_tsv
ON search_documents USING gin(intent_tsv);

-- Rejoin every reason of a node into the one document the old index held. The
-- trigger recomputes intent_tsv on the update.
UPDATE search_documents sd
SET intent_content = COALESCE((
    SELECT string_agg(r.content, ' ' ORDER BY r.id)
    FROM search_reasons r
    WHERE r.namespace = sd.namespace AND r.node_id = sd.node_id
), '');

DROP INDEX IF EXISTS idx_search_reasons_tsv;
DROP TRIGGER IF EXISTS trg_search_reasons_tsv ON search_reasons;
DROP FUNCTION IF EXISTS search_reasons_tsv_trigger();
DROP INDEX IF EXISTS idx_searchreason_ns_node;
DROP TABLE IF EXISTS search_reasons;
