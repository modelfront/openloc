-- +goose Up
-- A locked segment is always Approved or Auto-approved: locking is a
-- manager's approval of the text. Existing locked segments with text become
-- Approved as a person's decision; auto-approved and already approved ones
-- keep their status. A locked segment with no text cannot be approved, so it
-- is unlocked. Nothing is written to TM here.
UPDATE translations
SET status = 'approved', confirmed_via = 'user', updated_at = now()
WHERE locked
  AND coalesce(active_text, '') <> ''
  AND status NOT IN ('approved', 'auto_approved');

UPDATE translations
SET locked = false, updated_at = now()
WHERE locked
  AND coalesce(active_text, '') = '';

-- +goose Down
-- Data only. The statuses and locks before the Up are not kept, so there is
-- nothing to restore; the rows stay as the Up left them.
SELECT 1;
