-- Typo-tolerant fuzzy symbol search has been removed. Both callers of search are
-- driven by an agent quoting identifiers out of code it has already read, so a
-- query matching nothing exactly is naming something that does not exist, and an
-- approximate answer only turns that into a confident wrong one. The indexes are
-- dropped because nothing reads them and they still cost every node write.
--
-- The pg_trgm extension itself is left in place: it may be shared with other
-- schemas in the same database, and dropping it is not this migration's call.
DROP INDEX IF EXISTS idx_nodes_qualified_name_trgm;
DROP INDEX IF EXISTS idx_nodes_name_trgm;
