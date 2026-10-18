-- +goose Up
-- Skills are not master data: every character has an upper (1) and a lower (2) skill, and a
-- build's skill order refers to them as {"characterId", "slot"} inside builds.team.
DROP TABLE skills;

-- +goose Down
CREATE TABLE skills (
  id           text PRIMARY KEY CHECK (id ~ '^[1-9][0-9]{0,8}$'),
  character_id text NOT NULL REFERENCES characters (id),
  slot         smallint NOT NULL CHECK (slot IN (1, 2)),
  name_th      text NOT NULL,
  name_en      text NOT NULL DEFAULT '',
  image_path   text,
  active       boolean NOT NULL DEFAULT true,
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX skills_character ON skills (character_id);
