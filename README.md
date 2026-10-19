# 7K Re Community — API

The web client is a separate repo (`web`); in development its Vite server proxies `/api` here.

Go (net/http) + PostgreSQL 18. Business rules follow the FR document; section numbers appear in the code.

## Run

```bash
docker compose up -d db          # Postgres on localhost:5433 (dev + mama7k_test)
go run ./cmd/seed                # reset dev DB: sample users + groups (password123), master data empty
go run ./cmd/seed -sample        # ...plus sample game data (master data, lineups, builds, votes)
go run ./cmd/server              # http://localhost:8787, applies migrations on start
go test ./...                    # integration tests against mama7k_test
go run ./cmd/useradmin -username you -name "Name" -role SUPER_ADMIN   # create staff / change role
```

Configuration: copy `.env.example` to `.env` (git-ignored); every setting is listed there with its local default —
`DATABASE_URL`, `ADDR`/`PORT`, `COOKIE_SECURE`, `SESSION_TTL`, `MEDIA_PUBLIC_URL`, `UPLOAD_DIR`, and for production
`WEB_DIR`, `TRUSTED_PROXY_HOPS`, `R2_*`. Real environment variables win over `.env`. Tests use `TEST_DATABASE_URL`
(default: the `mama7k_test` database). Deploying: see [`docs/deploy.md`](docs/deploy.md); master data CSVs are in
[`docs/master-data/`](docs/master-data/).

## Design notes

- **Auth:** username + password; the site sends no email. httpOnly `SameSite=Lax` session cookie; only the sha256 of the
  token is stored. A forgotten password is reset by a Super Admin, who gets a temporary password to pass on; the user
  must choose a new one after signing in (`mustChangePassword`). Suspending a user or resetting a password deletes their
  sessions. State-changing requests must be `application/json` (CSRF guard).
- **Rate limits** (per client IP, in memory): login 10 / 5 min, register 5 / hour, other writes 120 / min → 429
  `RATE_LIMITED`.
- **Access rule (FR 4.6):** one SQL predicate, `visibleSQL` in `internal/app/builds_read.go`, used by every build read.
  Inaccessible builds return the same 404 as missing ones.
- **Builds:** sharing, votes and enemy pets are relational; the team (placements, pets, skill order, speed,
  per-character sets/stats/rings) is one JSONB document validated against master data on write.
- **Master data:** the id is a running number per type (`"1"`, `"2"` …), assigned on create (max + 1) and never
  changed; rows are deactivated, never deleted. Each item has `nameTh` (shown to users) and an optional `nameEn`.
  Types: characters, pets, sets, rings, stats. Skills are not master data — a build's skill order is a list of
  `{characterId, slot}` (1 = upper, 2 = lower).
- **Lineup dedupe:** unique `canonical_key` (formation + sorted characters per row) with `ON CONFLICT`.

## Endpoints

| Area | Method & path |
| --- | --- |
| Auth | `POST /api/auth/register`, `/login`, `/logout` (`{"username", "password"}`) |
| Profile | `GET /api/me` (null for guests), `PATCH /api/me`, `DELETE /api/me`, `POST /api/me/password` |
| Master data | `GET /api/master` |
| Feedback | `POST /api/feedback` (anyone, 5 / 10 min per IP); staff: `GET /api/admin/feedback?status=open\|done\|all`, `PATCH\|DELETE /api/admin/feedback/{id}` |
| Lineups | `GET /api/lineups?characters=a,b`, `POST /api/lineups`, `GET /api/lineups/{id}`, `GET /api/lineups/{id}/builds?enemyPet=&sort=WIN14\|VOTES\|NEWEST` |
| Builds | `GET /api/me/builds`, `POST /api/builds`, `GET /api/builds/{id}`, `PUT /api/builds/{id}`, `DELETE /api/builds/{id}` |
| Votes | `PUT /api/builds/{id}/vote`, `DELETE /api/builds/{id}/vote` |
| Groups | `GET /api/groups`, `POST /api/groups`, `POST /api/groups/join`, `GET\|PATCH\|DELETE /api/groups/{id}`, `POST /api/groups/{id}/leave`, `/transfer`, `/invite-code`, `DELETE /api/groups/{id}/members/{userId}`, `PUT /api/groups/{id}/builds/{buildId}/hidden` |
| Admin | `GET /api/admin/users`, `PATCH /api/admin/users/{id}`, `POST /api/admin/users/{id}/reset-password`, `GET /api/admin/builds`, `PATCH\|DELETE /api/admin/builds/{id}`, `GET /api/admin/lineups`, `PUT\|DELETE /api/admin/lineups/{id}`, `POST /api/admin/lineups/{id}/merge`, `POST /api/admin/master/{entity}`, `PUT\|PATCH /api/admin/master/{entity}/{id}`, `POST /api/admin/master/{entity}/{id}/image`, `POST /api/admin/master/{entity}/import/preview\|commit`, `GET /api/admin/audit` |

Errors: `{"error": {"code": "NOT_FOUND", "message": "..."}}` with codes `VALIDATION` 400, `UNAUTHENTICATED` 401,
`INVALID_CREDENTIALS` 401, `FORBIDDEN` / `SUSPENDED` 403, `NOT_FOUND` 404,
`USERNAME_TAKEN` / `DUPLICATE` / `HAS_BUILDS` / `OWNER_MUST_TRANSFER` 409, `RATE_LIMITED` 429.
