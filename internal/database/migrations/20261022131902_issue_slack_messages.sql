-- +goose Up
-- The Slack message that announced an issue to the support team. The
-- reporter's replies are posted in its thread.
CREATE TABLE issue_slack_messages (
    issue_id bigint PRIMARY KEY REFERENCES issues(id) ON DELETE CASCADE,
    channel text NOT NULL,
    -- Slack's id of the message (its "ts"), which is also the thread's id.
    ts text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE issue_slack_messages;
