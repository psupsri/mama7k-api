-- +goose Up
-- Master ids become running numbers per type ("1", "2", …) and the alias list becomes a
-- single English name. Ids stay text so builds.team, counter_pets and lineup keys are
-- unchanged in shape. Run on an empty master data set (the old "char_…" ids fail the check).

ALTER TABLE characters     DROP CONSTRAINT characters_id_check,     ADD CONSTRAINT characters_id_check     CHECK (id ~ '^[1-9][0-9]{0,8}$');
ALTER TABLE skills         DROP CONSTRAINT skills_id_check,         ADD CONSTRAINT skills_id_check         CHECK (id ~ '^[1-9][0-9]{0,8}$');
ALTER TABLE pets           DROP CONSTRAINT pets_id_check,           ADD CONSTRAINT pets_id_check           CHECK (id ~ '^[1-9][0-9]{0,8}$');
ALTER TABLE equipment_sets DROP CONSTRAINT equipment_sets_id_check, ADD CONSTRAINT equipment_sets_id_check CHECK (id ~ '^[1-9][0-9]{0,8}$');
ALTER TABLE rings          DROP CONSTRAINT rings_id_check,          ADD CONSTRAINT rings_id_check          CHECK (id ~ '^[1-9][0-9]{0,8}$');
ALTER TABLE stat_types     DROP CONSTRAINT stat_types_id_check,     ADD CONSTRAINT stat_types_id_check     CHECK (id ~ '^[1-9][0-9]{0,8}$');

ALTER TABLE characters     DROP COLUMN aliases, ADD COLUMN name_en text NOT NULL DEFAULT '';
ALTER TABLE skills         DROP COLUMN aliases, ADD COLUMN name_en text NOT NULL DEFAULT '';
ALTER TABLE pets           DROP COLUMN aliases, ADD COLUMN name_en text NOT NULL DEFAULT '';
ALTER TABLE equipment_sets DROP COLUMN aliases, ADD COLUMN name_en text NOT NULL DEFAULT '';
ALTER TABLE rings          DROP COLUMN aliases, ADD COLUMN name_en text NOT NULL DEFAULT '';
ALTER TABLE stat_types     DROP COLUMN aliases, ADD COLUMN name_en text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE characters DROP COLUMN name_en, ADD COLUMN aliases text[] NOT NULL DEFAULT '{}',
  DROP CONSTRAINT characters_id_check, ADD CONSTRAINT characters_id_check CHECK (id ~ '^char_[a-z0-9_]+$');
ALTER TABLE skills DROP COLUMN name_en, ADD COLUMN aliases text[] NOT NULL DEFAULT '{}',
  DROP CONSTRAINT skills_id_check, ADD CONSTRAINT skills_id_check CHECK (id ~ '^skill_[a-z0-9_]+$');
ALTER TABLE pets DROP COLUMN name_en, ADD COLUMN aliases text[] NOT NULL DEFAULT '{}',
  DROP CONSTRAINT pets_id_check, ADD CONSTRAINT pets_id_check CHECK (id ~ '^pet_[a-z0-9_]+$');
ALTER TABLE equipment_sets DROP COLUMN name_en, ADD COLUMN aliases text[] NOT NULL DEFAULT '{}',
  DROP CONSTRAINT equipment_sets_id_check, ADD CONSTRAINT equipment_sets_id_check CHECK (id ~ '^set_[a-z0-9_]+$');
ALTER TABLE rings DROP COLUMN name_en, ADD COLUMN aliases text[] NOT NULL DEFAULT '{}',
  DROP CONSTRAINT rings_id_check, ADD CONSTRAINT rings_id_check CHECK (id ~ '^ring_[a-z0-9_]+$');
ALTER TABLE stat_types DROP COLUMN name_en, ADD COLUMN aliases text[] NOT NULL DEFAULT '{}',
  DROP CONSTRAINT stat_types_id_check, ADD CONSTRAINT stat_types_id_check CHECK (id ~ '^stat_[a-z0-9_]+$');
