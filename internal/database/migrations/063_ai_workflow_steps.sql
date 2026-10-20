-- +goose Up
-- QE+APE AI workflow steps replace the legacy Predict feature.
--
--  * predict_on_upload retired — QP+APE no longer runs as an upload phase.
--    Instead a QE+APE workflow step runs ModelFront /predict automatically when
--    the job reaches it (predict-on-start), and /feedback when it completes.
--  * ai_only retired — an AI step is now an ordinary human-assigned step that
--    also carries an ai_engine_id (QE+APE). There is no human-less step; the
--    engine augments the assignee's step rather than replacing them.
--
-- ModelFront credentials moved to the ai_providers registry; the
-- legacy tenants.modelfront_api_key / _base_url columns are left in place and
-- backfilled into ai_providers at startup, to be dropped in a later migration
-- once every environment has run the backfill.
ALTER TABLE settings_profiles DROP COLUMN predict_on_upload;
ALTER TABLE workflow_steps DROP COLUMN ai_only;

-- +goose Down
ALTER TABLE settings_profiles ADD COLUMN predict_on_upload boolean NOT NULL DEFAULT false;
ALTER TABLE workflow_steps ADD COLUMN ai_only boolean NOT NULL DEFAULT false;
