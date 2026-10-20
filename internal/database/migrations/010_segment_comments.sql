-- +goose Up
CREATE TABLE segment_comments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    translation_id BIGINT NOT NULL REFERENCES translations(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_segment_comments_translation ON segment_comments(translation_id);

-- +goose Down
DROP TABLE IF EXISTS segment_comments;
