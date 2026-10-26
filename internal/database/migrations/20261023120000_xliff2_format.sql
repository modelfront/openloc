-- +goose Up
-- XLIFF 2.x source files (XLIFF Core 2.0 and 2.1). Distinct from 'xliff',
-- which stays reserved for XLIFF 1.2 and its Trados, Phrase and memoQ
-- variants; DetectFormat routes a document by its root version.
-- IF NOT EXISTS keeps this re-runnable: the Down below can't remove the
-- label, so a down+re-up cycle would otherwise fail with "enum label already
-- exists".
ALTER TYPE file_format ADD VALUE IF NOT EXISTS 'xliff2';

-- +goose Down
-- PostgreSQL does not support removing enum values.
-- To roll back, recreate the enum without 'xliff2' (requires dropping dependent columns).
