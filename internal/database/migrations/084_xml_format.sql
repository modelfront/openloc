-- +goose Up
-- Generic XML source files. Distinct from 'android_xml', which stays
-- reserved for <resources> string files; DetectFormat routes .xml uploads to
-- one or the other by root element.
-- IF NOT EXISTS keeps this re-runnable. The Down below can't remove the label,
-- so a down+re-up cycle would otherwise fail with "enum label already exists" —
-- which is why migration 007 needed an entry in migrations_test.go's
-- skipDownReUp list. This one doesn't.
ALTER TYPE file_format ADD VALUE IF NOT EXISTS 'xml';

-- +goose Down
-- PostgreSQL does not support removing enum values.
-- To roll back, recreate the enum without 'xml' (requires dropping dependent columns).
