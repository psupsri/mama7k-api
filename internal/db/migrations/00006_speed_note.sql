-- +goose Up
-- The speed rule is now FASTER / SLOWER / not specified plus a free-text note. The old
-- "at least N" rule becomes a note.
UPDATE builds
SET team = jsonb_set(team, '{speed}', jsonb_build_object('note', 'Speed อย่างน้อย ' || (team #>> '{speed,value}')))
WHERE team #>> '{speed,kind}' = 'MIN';

-- +goose Down
UPDATE builds
SET team = jsonb_set(team, '{speed}', 'null'::jsonb)
WHERE team #>> '{speed,kind}' IS NULL AND team #> '{speed}' IS NOT NULL AND jsonb_typeof(team #> '{speed}') = 'object';
