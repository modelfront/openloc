-- +goose Up
-- Org signup approval lifecycle. Existing tenants default to
-- 'approved' so nothing that works today is gated. New self-serve signups are
-- created 'pending' and blocked by auth.Middleware until a platform operator
-- approves them; 'rejected' stays gated (kept for audit rather than deleted).
ALTER TABLE tenants ADD COLUMN status TEXT NOT NULL DEFAULT 'approved';

-- Operator "pending signups" list — a small, hot-when-nonempty slice.
CREATE INDEX idx_tenants_pending ON tenants (created_at) WHERE status = 'pending';

-- +goose Down
DROP INDEX IF EXISTS idx_tenants_pending;
ALTER TABLE tenants DROP COLUMN IF EXISTS status;
