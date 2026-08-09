DROP INDEX IF EXISTS idx_search_documents_intent_tsv;
DROP TRIGGER IF EXISTS trg_search_documents_intent_tsv ON search_documents;
DROP FUNCTION IF EXISTS search_documents_intent_tsv_trigger();
ALTER TABLE search_documents DROP COLUMN IF EXISTS intent_tsv;
ALTER TABLE search_documents DROP COLUMN IF EXISTS intent_content;
