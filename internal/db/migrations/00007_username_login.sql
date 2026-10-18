-- +goose Up
-- Accounts sign in with a username instead of an email: the site runs without an email
-- service, so there is no email verification, and a Super Admin resets forgotten passwords.
ALTER TABLE users ADD COLUMN username citext;

-- Existing accounts get the part of their email before "@", cleaned up to the username rule;
-- a clash gets a running suffix.
WITH base AS (
  SELECT id, created_at,
         left(lower(regexp_replace(split_part(email::text, '@', 1), '[^a-zA-Z0-9_]', '_', 'g')), 16) AS b
  FROM users
), named AS (
  SELECT id, CASE WHEN length(b) < 3 THEN 'user_' || b ELSE b END AS b, created_at FROM base
), numbered AS (
  SELECT id, b, row_number() OVER (PARTITION BY b ORDER BY created_at) AS n FROM named
)
UPDATE users u SET username = CASE WHEN n = 1 THEN numbered.b ELSE numbered.b || '_' || n END
FROM numbered WHERE numbered.id = u.id;

ALTER TABLE users
  ALTER COLUMN username SET NOT NULL,
  ADD CONSTRAINT users_username_check CHECK (username ~ '^[a-z0-9_]{3,20}$');
-- A username can be taken again after the old account is deleted.
CREATE UNIQUE INDEX users_username_active ON users (username) WHERE deleted_at IS NULL;

-- Set when a Super Admin resets the password (or the account is created from the command line);
-- the web asks the user to choose a new password before anything else.
ALTER TABLE users ADD COLUMN must_change_password boolean NOT NULL DEFAULT false;

DROP INDEX users_email_active;
ALTER TABLE users DROP COLUMN email, DROP COLUMN email_verified_at;
DROP TABLE email_tokens;
DROP TYPE email_token_kind;

-- +goose Down
CREATE TYPE email_token_kind AS ENUM ('VERIFY_EMAIL', 'RESET_PASSWORD');
CREATE TABLE email_tokens (
  token_hash bytea PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind       email_token_kind NOT NULL,
  expires_at timestamptz NOT NULL,
  used_at    timestamptz
);
ALTER TABLE users ADD COLUMN email citext, ADD COLUMN email_verified_at timestamptz;
UPDATE users SET email = username || '@example.invalid', email_verified_at = now();
ALTER TABLE users ALTER COLUMN email SET NOT NULL;
CREATE UNIQUE INDEX users_email_active ON users (email) WHERE deleted_at IS NULL;
DROP INDEX users_username_active;
ALTER TABLE users DROP COLUMN username, DROP COLUMN must_change_password;
