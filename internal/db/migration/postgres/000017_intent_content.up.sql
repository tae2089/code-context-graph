ALTER TABLE search_documents ADD COLUMN IF NOT EXISTS intent_content text NOT NULL DEFAULT '';

-- The name index and the intent index must not share a vector. Searching the
-- reason a node exists is the whole point of this column, and a combined tsv
-- would put the identifier tokens back in front of it.
CREATE OR REPLACE FUNCTION search_documents_intent_tsv_trigger() RETURNS trigger AS $$
BEGIN
    NEW.intent_tsv := to_tsvector('simple', coalesce(NEW.intent_content, ''));
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

ALTER TABLE search_documents ADD COLUMN IF NOT EXISTS intent_tsv tsvector;

DROP TRIGGER IF EXISTS trg_search_documents_intent_tsv ON search_documents;
CREATE TRIGGER trg_search_documents_intent_tsv
BEFORE INSERT OR UPDATE ON search_documents
FOR EACH ROW EXECUTE FUNCTION search_documents_intent_tsv_trigger();

CREATE INDEX IF NOT EXISTS idx_search_documents_intent_tsv
ON search_documents USING gin(intent_tsv);
