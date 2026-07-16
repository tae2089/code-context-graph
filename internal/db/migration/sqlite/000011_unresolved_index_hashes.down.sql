DELETE FROM unresolved_edge_candidates;
DELETE FROM unresolved_index_states;

DROP INDEX IF EXISTS idx_unresolved_ns_fp_hash;
DROP INDEX IF EXISTS idx_unresolved_lookup_hash;

ALTER TABLE unresolved_edge_candidates
DROP COLUMN fingerprint_hash;
ALTER TABLE unresolved_edge_candidates
DROP COLUMN lookup_key_hash;

CREATE UNIQUE INDEX idx_unresolved_ns_fp
ON unresolved_edge_candidates(namespace, fingerprint);
CREATE INDEX idx_unresolved_edge_candidates_lookup_key
ON unresolved_edge_candidates(lookup_key);
