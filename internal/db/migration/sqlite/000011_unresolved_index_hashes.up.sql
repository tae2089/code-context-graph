DELETE FROM unresolved_edge_candidates;
DELETE FROM unresolved_index_states;

DROP INDEX IF EXISTS idx_unresolved_ns_fp;
DROP INDEX IF EXISTS idx_unresolved_edge_candidates_lookup_key;

ALTER TABLE unresolved_edge_candidates
ADD COLUMN lookup_key_hash text NOT NULL DEFAULT '';
ALTER TABLE unresolved_edge_candidates
ADD COLUMN fingerprint_hash text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX idx_unresolved_ns_fp_hash
ON unresolved_edge_candidates(namespace, fingerprint_hash);
CREATE INDEX idx_unresolved_lookup_hash
ON unresolved_edge_candidates(lookup_key_hash);
