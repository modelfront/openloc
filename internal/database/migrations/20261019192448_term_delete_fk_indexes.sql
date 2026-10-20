-- +goose NO TRANSACTION
-- +goose Up
-- Full indexes on the foreign keys an organization delete walks: concepts by
-- term base, live and soft-deleted alike, and terms by concept for the
-- cascade from each deleted concept. The existing indexes on these columns
-- hold live rows only, so they serve neither lookup.
--
-- CONCURRENTLY keeps writes flowing while each index builds, and cannot run
-- inside a transaction. A failed concurrent build leaves an invalid index
-- behind, so each one is dropped first: a re-run after a partial failure
-- builds it again.
DROP INDEX CONCURRENTLY IF EXISTS idx_term_concept_term_base_all;
CREATE INDEX CONCURRENTLY idx_term_concept_term_base_all ON term_concept (term_base_id);

DROP INDEX CONCURRENTLY IF EXISTS idx_term_concept_id;
CREATE INDEX CONCURRENTLY idx_term_concept_id ON term (concept_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_term_concept_term_base_all;
DROP INDEX CONCURRENTLY IF EXISTS idx_term_concept_id;
