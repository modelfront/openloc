-- +goose Up
-- Collapse the six-role set (owner/admin/member/translator/reviewer/
-- vendor) into the four-role model (owner/admin/project_manager/linguist).
--   - vendor / translator / reviewer  → linguist  (all restricted linguists)
--   - member                          → project_manager (broad non-owner access)
--   - owner / admin                   → unchanged
-- Roles are a plain text column (no enum/CHECK), so a data UPDATE is all that's
-- needed. workflow_steps.role_required is remapped the same way so step gating
-- keeps working.
UPDATE users SET role = 'linguist'
    WHERE role IN ('vendor', 'translator', 'reviewer');
UPDATE users SET role = 'project_manager'
    WHERE role = 'member';

UPDATE invites SET role = 'linguist'
    WHERE role IN ('vendor', 'translator', 'reviewer');
UPDATE invites SET role = 'project_manager'
    WHERE role = 'member';

UPDATE workflow_steps SET role_required = 'linguist'
    WHERE role_required IN ('vendor', 'translator', 'reviewer');
UPDATE workflow_steps SET role_required = 'project_manager'
    WHERE role_required = 'member';

-- +goose Down
-- Best-effort reversal: linguist → member, project_manager → member. The
-- original member/translator/reviewer/vendor distinctions are lost (collapsed on
-- Up), so everything non-owner/admin becomes 'member' again.
UPDATE users SET role = 'member' WHERE role IN ('linguist', 'project_manager');
UPDATE invites SET role = 'member' WHERE role IN ('linguist', 'project_manager');
UPDATE workflow_steps SET role_required = 'member' WHERE role_required IN ('linguist', 'project_manager');
