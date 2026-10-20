-- +goose Up
-- +goose StatementBegin

-- Project-level Client + Domain taxonomy.
--
-- Free-text, optional labels surfaced in the project create wizard
-- alongside source/target languages. Forwarded to ModelFront as
-- additional_metadata.{client,domain} on /predict + /feedback so QP+APE
-- can route by customer industry vertical. "Client" and "Domain" are
-- project taxonomy independent of the Customer FK (Customer is a business
-- entity, Client/Domain are free-form classifiers used by MT/QP routing and
-- reporting).
--
-- Stored as NULLABLE text. Empty string and NULL are treated equivalently
-- (server-side trim in the handler). No index — these aren't filter
-- columns (the UI doesn't filter projects by client/domain today; if we
-- add that later we'll add a partial index then).
--
-- The Customer FK column from migration 025 is intentionally NOT replaced.
-- Customer = relationship entity (with name, contacts, billing). Client
-- here = free-text label that can equal customer.name or be empty or be
-- "Confidential" / "Internal" — semantics differ.

ALTER TABLE projects
    ADD COLUMN client text,
    ADD COLUMN domain text;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE projects
    DROP COLUMN IF EXISTS domain,
    DROP COLUMN IF EXISTS client;

-- +goose StatementEnd
