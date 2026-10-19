-- +goose Up
-- Feedback anyone can send from the site (guests too); only staff can read it.
CREATE TYPE feedback_kind AS ENUM ('BUG', 'IDEA', 'DATA', 'OTHER');

CREATE TABLE feedback (
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  kind        feedback_kind NOT NULL,
  message     text NOT NULL CHECK (length(message) BETWEEN 1 AND 2000),
  contact     text NOT NULL DEFAULT '' CHECK (length(contact) <= 100), -- how to reply, e.g. a Discord name
  page        text NOT NULL DEFAULT '' CHECK (length(page) <= 300),    -- path the sender was on
  user_id     uuid REFERENCES users (id) ON DELETE SET NULL,           -- NULL = sent as a guest
  created_at  timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz,
  resolved_by uuid REFERENCES users (id) ON DELETE SET NULL
);
CREATE INDEX feedback_created ON feedback (created_at DESC);

-- +goose Down
DROP TABLE feedback;
DROP TYPE feedback_kind;
