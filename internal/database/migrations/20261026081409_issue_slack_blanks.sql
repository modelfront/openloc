-- +goose Up
-- Every deleted issue_slack_messages row queues its Slack card here, whatever
-- deleted it (an org or account delete cascades through issues), and a soft
-- delete queues its issue's card too. A sweep blanks each queued card and
-- then drops its row, so a card Slack could not update is tried again on the
-- next run.
CREATE TABLE issue_slack_blanks (
    issue_id bigint PRIMARY KEY,
    channel text NOT NULL,
    ts text NOT NULL,
    queued_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION queue_issue_slack_blank() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO issue_slack_blanks (issue_id, channel, ts)
    VALUES (OLD.issue_id, OLD.channel, OLD.ts)
    ON CONFLICT (issue_id) DO NOTHING;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER issue_slack_messages_queue_blank
AFTER DELETE ON issue_slack_messages
FOR EACH ROW EXECUTE FUNCTION queue_issue_slack_blank();

-- +goose Down
DROP TRIGGER issue_slack_messages_queue_blank ON issue_slack_messages;
DROP FUNCTION queue_issue_slack_blank();
DROP TABLE issue_slack_blanks;
