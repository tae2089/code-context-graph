CREATE OR REPLACE FUNCTION search_documents_tsv_trigger() RETURNS trigger AS $$
BEGIN
    NEW.tsv := to_tsvector('simple', COALESCE(NEW.content, ''));
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION search_documents_intent_tsv_trigger() RETURNS trigger AS $$
BEGIN
    NEW.intent_tsv := to_tsvector('simple', coalesce(NEW.intent_content, ''));
    RETURN NEW;
END
$$ LANGUAGE plpgsql;

UPDATE search_documents SET content = content;
