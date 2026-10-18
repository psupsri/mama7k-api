-- +goose Up
-- The site recommends teams; it is not a game database. Master items keep only names and an image.
ALTER TABLE equipment_sets DROP COLUMN effect_2, DROP COLUMN effect_4;
ALTER TABLE rings          DROP COLUMN rarity, DROP COLUMN effect;
ALTER TABLE stat_types     DROP COLUMN unit;
DROP TYPE ring_rarity;
DROP TYPE stat_unit;

-- +goose Down
CREATE TYPE ring_rarity AS ENUM ('LEGENDARY', 'RARE', 'UNCOMMON', 'COMMON');
CREATE TYPE stat_unit AS ENUM ('PERCENT', 'NUMBER');
ALTER TABLE equipment_sets ADD COLUMN effect_2 text NOT NULL DEFAULT '', ADD COLUMN effect_4 text NOT NULL DEFAULT '';
ALTER TABLE rings          ADD COLUMN rarity ring_rarity NOT NULL DEFAULT 'COMMON', ADD COLUMN effect text NOT NULL DEFAULT '';
ALTER TABLE stat_types     ADD COLUMN unit stat_unit NOT NULL DEFAULT 'PERCENT';
