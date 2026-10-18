# 7K Re Community — API

Go (net/http) + PostgreSQL 18. Business rules follow the FR document; section numbers appear in the code.

## Run

```bash
docker compose up -d db          # from the repo root; Postgres on localhost:5433 (dev + mama7k_test)
cd api
go run ./cmd/seed                # reset dev DB and load sample data (password: password123)
go run ./cmd/server              # http://localhost:8080, applies migrations on start
go test ./...                    # integration tests against mama7k_test
```

Environment: `ADDR` (`:8080`), `DATABASE_URL`, `BASE_URL` (web origin for email links), `COOKIE_SECURE` (`true` behind HTTPS),
`UPLOAD_DIR` (`./uploads`), `TEST_DATABASE_URL` (tests).

## Design notes

- **Auth:** httpOnly `SameSite=Lax` session cookie; only the sha256 of the token is stored. Suspending a user or resetting a
  password deletes their sessions. State-changing requests must be `application/json` (CSRF guard).
- **Access rule (FR 4.6):** one SQL predicate, `visibleSQL` in `internal/app/builds_read.go`, used by every build read.
  Inaccessible builds return the same 404 as missing ones.
- **Builds:** sharing, votes and enemy pets are relational; the team (placements, pets, skill order, speed,
  per-character sets/stats/rings) is one JSONB document validated against master data on write.
- **Master data:** the code is the primary key and never changes; rows are deactivated, never deleted.
- **Lineup dedupe:** unique `canonical_key` (formation + sorted characters per row) with `ON CONFLICT`.

## Endpoints

| Area | Method & path |
| --- | --- |
| Auth | `POST /api/auth/register`, `/login`, `/logout`, `/verify-email`, `/resend-verification`, `/password-reset`, `/password-reset/confirm` |
| Profile | `GET /api/me` (null for guests), `PATCH /api/me`, `DELETE /api/me` |
| Master data | `GET /api/master` |
| Lineups | `GET /api/lineups?characters=a,b`, `POST /api/lineups`, `GET /api/lineups/{id}`, `GET /api/lineups/{id}/builds?enemyPet=&sort=WIN14\|VOTES\|NEWEST` |
| Builds | `GET /api/me/builds`, `POST /api/builds`, `GET /api/builds/{id}`, `PUT /api/builds/{id}`, `DELETE /api/builds/{id}` |
| Votes | `PUT /api/builds/{id}/vote`, `DELETE /api/builds/{id}/vote` |
| Groups | `GET /api/groups`, `POST /api/groups`, `POST /api/groups/join`, `GET\|PATCH\|DELETE /api/groups/{id}`, `POST /api/groups/{id}/leave`, `/transfer`, `/invite-code`, `DELETE /api/groups/{id}/members/{userId}`, `PUT /api/groups/{id}/builds/{buildId}/hidden` |
| Admin | `GET /api/admin/users`, `PATCH /api/admin/users/{id}`, `GET /api/admin/builds`, `PATCH\|DELETE /api/admin/builds/{id}`, `GET /api/admin/lineups`, `PUT\|DELETE /api/admin/lineups/{id}`, `POST /api/admin/lineups/{id}/merge`, `PUT\|PATCH /api/admin/master/{entity}/{code}`, `POST /api/admin/master/{entity}/{code}/image`, `POST /api/admin/master/{entity}/import/preview\|commit`, `GET /api/admin/audit` |

Errors: `{"error": {"code": "NOT_FOUND", "message": "..."}}` with codes `VALIDATION` 400, `UNAUTHENTICATED` 401,
`INVALID_CREDENTIALS` 401, `FORBIDDEN` / `SUSPENDED` / `EMAIL_NOT_VERIFIED` 403, `NOT_FOUND` 404,
`EMAIL_TAKEN` / `DUPLICATE` / `HAS_BUILDS` / `OWNER_MUST_TRANSFER` 409.
