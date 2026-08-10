-- Make the tsvector see the tokens SQLite's FTS5 sees. unicode61 splits
-- indexed text on every non-alphanumeric rune, so on SQLite the qualified name
-- `webhook.WebhookHandler.verifySignature` answers the query `webhookhandler`.
-- PostgreSQL's default parser instead keeps the whole dotted name as one
-- host-like token and a file path as one file token, so the same document
-- answered fewer queries on PostgreSQL. Translating the separator runes to
-- spaces before to_tsvector gives both backends the same token stream.
CREATE OR REPLACE FUNCTION search_documents_tsv_trigger() RETURNS trigger AS $$
BEGIN
    NEW.tsv := to_tsvector('simple', translate(COALESCE(NEW.content, ''), '/._', '   '));
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION search_documents_intent_tsv_trigger() RETURNS trigger AS $$
BEGIN
    NEW.intent_tsv := to_tsvector('simple', translate(coalesce(NEW.intent_content, ''), '/._', '   '));
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

-- Reindex every stored document under the new tokenization. The triggers fire
-- on UPDATE and recompute both vectors, so touching the rows is enough.
UPDATE search_documents SET content = content;
