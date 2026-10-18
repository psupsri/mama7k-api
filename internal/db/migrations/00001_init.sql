-- +goose Up
-- Initial schema for the 7K Re community site. Section numbers refer to the FR document.

CREATE EXTENSION IF NOT EXISTS citext;

CREATE TYPE user_role AS ENUM ('USER', 'MODERATOR', 'SUPER_ADMIN');
CREATE TYPE formation_type AS ENUM ('F3B2', 'F2B3', 'F1B4', 'F4B1');
CREATE TYPE formation_row AS ENUM ('FRONT', 'BACK');
CREATE TYPE visibility AS ENUM ('PUBLIC', 'GROUP', 'PRIVATE');
CREATE TYPE battle_result AS ENUM ('WIN', 'LOSS');
CREATE TYPE equip_level AS ENUM ('EXACT_EQUIPMENT', 'SAME_SET_DIFFERENT_SUBSTATS', 'CHARACTERS_ONLY');
CREATE TYPE ring_rarity AS ENUM ('LEGENDARY', 'RARE', 'UNCOMMON', 'COMMON');
CREATE TYPE stat_unit AS ENUM ('PERCENT', 'NUMBER');
CREATE TYPE email_token_kind AS ENUM ('VERIFY_EMAIL', 'RESET_PASSWORD');

-- ---------- Accounts (FR 2) ----------

CREATE TABLE users (
  id                uuid PRIMARY KEY DEFAULT uuidv7(),
  email             citext NOT NULL,
  password_hash     text NOT NULL,
  display_name      text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 50),
  role              user_role NOT NULL DEFAULT 'USER',
  suspended         boolean NOT NULL DEFAULT false,
  email_verified_at timestamptz,
  deleted_at        timestamptz,
  created_at        timestamptz NOT NULL DEFAULT now()
);
-- An email can be reused after the old account is deleted.
CREATE UNIQUE INDEX users_email_active ON users (email) WHERE deleted_at IS NULL;

CREATE TABLE sessions (
  token_hash   bytea PRIMARY KEY, -- sha256 of the cookie value; the raw token is never stored
  user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE email_tokens (
  token_hash bytea PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind       email_token_kind NOT NULL,
  expires_at timestamptz NOT NULL,
  used_at    timestamptz
);

-- ---------- Master data (FR 9). The code is the id and never changes; rows are never deleted. ----------

CREATE TABLE characters (
  id         text PRIMARY KEY CHECK (id ~ '^char_[a-z0-9_]+$'),
  name       text NOT NULL,
  aliases    text[] NOT NULL DEFAULT '{}',
  image_path text,
  active     boolean NOT NULL DEFAULT true,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE skills (
  id           text PRIMARY KEY CHECK (id ~ '^skill_[a-z0-9_]+$'),
  character_id text NOT NULL REFERENCES characters (id),
  slot         smallint NOT NULL CHECK (slot IN (1, 2)), -- 1 = upper, 2 = lower
  name         text NOT NULL,
  aliases      text[] NOT NULL DEFAULT '{}',
  image_path   text,
  active       boolean NOT NULL DEFAULT true,
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX skills_character ON skills (character_id);

CREATE TABLE pets (
  id         text PRIMARY KEY CHECK (id ~ '^pet_[a-z0-9_]+$'),
  name       text NOT NULL,
  aliases    text[] NOT NULL DEFAULT '{}',
  image_path text,
  active     boolean NOT NULL DEFAULT true,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE equipment_sets (
  id         text PRIMARY KEY CHECK (id ~ '^set_[a-z0-9_]+$'),
  name       text NOT NULL,
  aliases    text[] NOT NULL DEFAULT '{}',
  effect_2   text NOT NULL DEFAULT '',
  effect_4   text NOT NULL DEFAULT '',
  image_path text,
  active     boolean NOT NULL DEFAULT true,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE rings (
  id         text PRIMARY KEY CHECK (id ~ '^ring_[a-z0-9_]+$'),
  name       text NOT NULL,
  aliases    text[] NOT NULL DEFAULT '{}',
  rarity     ring_rarity NOT NULL,
  effect     text NOT NULL DEFAULT '',
  image_path text,
  active     boolean NOT NULL DEFAULT true,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE stat_types (
  id         text PRIMARY KEY CHECK (id ~ '^stat_[a-z0-9_]+$'),
  name       text NOT NULL,
  aliases    text[] NOT NULL DEFAULT '{}',
  unit       stat_unit NOT NULL DEFAULT 'PERCENT',
  image_path text,
  active     boolean NOT NULL DEFAULT true,
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- ---------- Groups (FR 5) ----------

CREATE TABLE groups (
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
  owner_id    uuid NOT NULL REFERENCES users (id),
  invite_code text NOT NULL UNIQUE,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE group_members (
  group_id  uuid NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
  user_id   uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  joined_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (group_id, user_id)
);
CREATE INDEX group_members_user ON group_members (user_id);

-- ---------- Defense lineups (FR 3): shared data, deduplicated by canonical key ----------

CREATE TABLE lineups (
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  formation     formation_type NOT NULL,
  -- formation + sorted characters per row, e.g. "F4B1|F:char_a,char_b|B:char_c"
  canonical_key text NOT NULL UNIQUE,
  created_by    uuid REFERENCES users (id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE lineup_placements (
  lineup_id    uuid NOT NULL REFERENCES lineups (id) ON DELETE CASCADE,
  character_id text NOT NULL REFERENCES characters (id),
  row          formation_row NOT NULL,
  slot         smallint NOT NULL CHECK (slot BETWEEN 0 AND 3),
  PRIMARY KEY (lineup_id, row, slot),
  UNIQUE (lineup_id, character_id)
);
CREATE INDEX lineup_placements_character ON lineup_placements (character_id);

-- ---------- Offense builds (FR 4) ----------

CREATE TABLE builds (
  id              uuid PRIMARY KEY DEFAULT uuidv7(),
  lineup_id       uuid NOT NULL REFERENCES lineups (id), -- never changes after creation
  creator_id      uuid REFERENCES users (id) ON DELETE SET NULL,
  creator_deleted boolean NOT NULL DEFAULT false,
  visibility      visibility NOT NULL,
  admin_hidden    boolean NOT NULL DEFAULT false,
  description     text NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
  -- Enemy pets this build counters. Empty = works against any pet (FR 4.2).
  counter_pets    text[] NOT NULL DEFAULT '{}',
  formation       formation_type NOT NULL,
  -- Team details read and written as a whole: placements, own pets, skill order, speed rule,
  -- per-character recommendations. Validated against master data by the API on write.
  team            jsonb NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX builds_lineup ON builds (lineup_id);
CREATE INDEX builds_creator ON builds (creator_id);
CREATE INDEX builds_counter_pets ON builds USING gin (counter_pets);

CREATE TABLE build_shares (
  build_id uuid NOT NULL REFERENCES builds (id) ON DELETE CASCADE,
  group_id uuid NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
  hidden   boolean NOT NULL DEFAULT false, -- set by the group owner (FR 5.2)
  PRIMARY KEY (build_id, group_id)
);
CREATE INDEX build_shares_group ON build_shares (group_id);

-- ---------- Votes (FR 6) ----------

CREATE TABLE votes (
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  build_id    uuid NOT NULL REFERENCES builds (id) ON DELETE CASCADE,
  user_id     uuid REFERENCES users (id) ON DELETE SET NULL, -- NULL = anonymised after account deletion
  result      battle_result NOT NULL,
  equip_level equip_level NOT NULL,
  voted_at    timestamptz NOT NULL DEFAULT now() -- updated when the vote changes (drives "last 14 days")
);
CREATE UNIQUE INDEX votes_one_per_user ON votes (build_id, user_id) WHERE user_id IS NOT NULL;
CREATE INDEX votes_build_time ON votes (build_id, voted_at);

-- ---------- Admin (FR 8.3) ----------

CREATE TABLE audit_log (
  id       bigserial PRIMARY KEY,
  actor_id uuid REFERENCES users (id) ON DELETE SET NULL,
  action   text NOT NULL,
  target   text NOT NULL,
  detail   jsonb,
  at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_at ON audit_log (at DESC);

-- +goose Down
DROP TABLE audit_log, votes, build_shares, builds, lineup_placements, lineups, group_members, groups,
  stat_types, rings, equipment_sets, pets, skills, characters, email_tokens, sessions, users;
DROP TYPE email_token_kind, stat_unit, ring_rarity, equip_level, battle_result, visibility,
  formation_row, formation_type, user_role;
