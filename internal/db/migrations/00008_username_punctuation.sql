-- +goose Up
-- Usernames may also use . - @ (common in sign-in names, e.g. an email-like name), up to 30.
ALTER TABLE users DROP CONSTRAINT users_username_check,
  ADD CONSTRAINT users_username_check CHECK (username ~ '^[a-z0-9._@-]{3,30}$');

-- +goose Down
ALTER TABLE users DROP CONSTRAINT users_username_check,
  ADD CONSTRAINT users_username_check CHECK (username ~ '^[a-z0-9_]{3,20}$');
