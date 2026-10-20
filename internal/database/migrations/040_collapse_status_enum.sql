-- +goose Up
-- Collapse the translation_status enum from 7 values to 3.
--
-- Surviving values: 'needs_review', 'auto_approved', 'approved'.
--
-- The four removed values become DERIVED predicates on other columns, so no
-- information is lost (the column was carrying three orthogonal concerns):
--   • 'untranslated'    → status='needs_review' AND active_text IS NULL
--   • 'pre_translated'  → status='needs_review' AND origin IS NOT NULL
--                          AND edited_by IS NULL AND active_text IS NOT NULL
--   • 'mt_pending' /    → in-flight pipeline state. These values had NO writer
--     'predict_pending'    (vestigial since the worker stopped stamping them);
--                          the in_flight_phase column (migration 037) is the
--                          forward-looking home for "MT/predict is in flight".
--                          It stays dormant until a loading-UX slice wires it.
--
-- All four collapse to 'needs_review' here. Postgres can't DROP enum values in
-- place, so we recreate the type and swap the column (house pattern, see
-- migration 038). The idx_translations_status index is rebuilt automatically
-- by ALTER COLUMN ... TYPE.

-- 1. Remap every doomed value to needs_review while the old enum still permits
--    them. untranslated rows already have active_text IS NULL; pre_translated
--    rows already have origin set + edited_by NULL — the derived predicates
--    keep distinguishing them post-collapse.
UPDATE translations
SET status = 'needs_review'
WHERE status IN ('untranslated', 'mt_pending', 'pre_translated', 'predict_pending');

-- 2. Swap the enum type.
ALTER TABLE translations ALTER COLUMN status DROP DEFAULT;

CREATE TYPE translation_status_new AS ENUM ('needs_review', 'auto_approved', 'approved');

ALTER TABLE translations
    ALTER COLUMN status TYPE translation_status_new
    USING status::text::translation_status_new;

ALTER TABLE translations
    ALTER COLUMN status SET DEFAULT 'needs_review'::translation_status_new;

DROP TYPE translation_status;
ALTER TYPE translation_status_new RENAME TO translation_status;

-- +goose Down
-- One-way for data: the four collapsed values can't be reconstructed from the
-- status column alone (they're now derived). This Down restores the 7-value
-- enum + the old default so the schema is parseable, but previously-collapsed
-- rows stay 'needs_review'. Rollback strategy for data is "restore from backup".

ALTER TABLE translations ALTER COLUMN status DROP DEFAULT;

CREATE TYPE translation_status_old AS ENUM (
    'untranslated',
    'mt_pending',
    'pre_translated',
    'predict_pending',
    'auto_approved',
    'needs_review',
    'approved'
);

ALTER TABLE translations
    ALTER COLUMN status TYPE translation_status_old
    USING status::text::translation_status_old;

ALTER TABLE translations
    ALTER COLUMN status SET DEFAULT 'untranslated'::translation_status_old;

DROP TYPE translation_status;
ALTER TYPE translation_status_old RENAME TO translation_status;
