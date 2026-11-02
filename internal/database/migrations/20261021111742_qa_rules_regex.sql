-- +goose Up
-- A profile holds at most one rule per built-in check type, and any number of
-- regex rules, each with its own pattern in config.
ALTER TABLE qa_rules DROP CONSTRAINT qa_rules_profile_id_check_type_key;
CREATE UNIQUE INDEX qa_rules_profile_check_type_key ON qa_rules (profile_id, check_type) WHERE check_type <> 'regex';

-- +goose Down
DELETE FROM qa_results WHERE check_type LIKE 'regex:%';
DELETE FROM qa_rules WHERE check_type = 'regex';
DROP INDEX IF EXISTS qa_rules_profile_check_type_key;
ALTER TABLE qa_rules ADD CONSTRAINT qa_rules_profile_id_check_type_key UNIQUE (profile_id, check_type);
