-- +goose Up
-- The display name of master items is the Thai community name; name it so beside name_en.
ALTER TABLE characters     RENAME COLUMN name TO name_th;
ALTER TABLE skills         RENAME COLUMN name TO name_th;
ALTER TABLE pets           RENAME COLUMN name TO name_th;
ALTER TABLE equipment_sets RENAME COLUMN name TO name_th;
ALTER TABLE rings          RENAME COLUMN name TO name_th;
ALTER TABLE stat_types     RENAME COLUMN name TO name_th;

-- +goose Down
ALTER TABLE characters     RENAME COLUMN name_th TO name;
ALTER TABLE skills         RENAME COLUMN name_th TO name;
ALTER TABLE pets           RENAME COLUMN name_th TO name;
ALTER TABLE equipment_sets RENAME COLUMN name_th TO name;
ALTER TABLE rings          RENAME COLUMN name_th TO name;
ALTER TABLE stat_types     RENAME COLUMN name_th TO name;
