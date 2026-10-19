# Deploying (free tier)

Two repos, two Render services. The browser only ever talks to the web site's domain: the static site forwards
`/api/*` to the API, so the session cookie is first-party and no CORS is needed.

| Part | Repo | Service | Notes |
| --- | --- | --- | --- |
| Web | `web` | Render **Static Site** (free) | Does not sleep. Rewrites `/api/*` to the API. |
| API | `api` (this repo) | Render **Web Service**, Docker, free | Sleeps after ~15 min idle; the first API call after that waits 30–60 s. |
| Database | — | Neon free Postgres | 0.5 GB. Use the **direct** connection string (host without `-pooler`). |
| Images | — | Cloudflare R2 (existing bucket) | Imported paths and admin-form uploads both live here. |

No email service is needed: accounts use a username, and a Super Admin resets forgotten passwords.

## 1. Accounts and keys

1. **GitHub** — push both repos.
2. **Neon** — create a project (region near Thailand, e.g. Singapore) → *Connection details* → turn **off** "Connection
   pooling" → copy the string, e.g.
   `postgresql://user:pass@ep-xxx.ap-southeast-1.aws.neon.tech/neondb?sslmode=require`.
3. **Cloudflare R2** → *Manage API tokens* → create a token with **Object Read & Write** on the bucket. Note the
   Account ID, Access Key ID and Secret Access Key (shown once).

## 2. API — Render Web Service

*New → Web Service →* the `api` repo. Render detects the `Dockerfile`. Choose the **Free** instance, set **Health Check
Path** to `/api/health`, and add:

| Variable | Value |
| --- | --- |
| `DATABASE_URL` | the Neon string from step 1 |
| `COOKIE_SECURE` | `true` |
| `MEDIA_PUBLIC_URL` | `https://pub-….r2.dev` (same as in `.env`) |
| `TRUSTED_PROXY_HOPS` | start with `2` (static site rewrite + Render's proxy), then check below |
| `R2_ACCOUNT_ID`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_BUCKET` | from step 1 |

Do not set `PORT`; Render provides it. The API applies database migrations on start. Note its URL, e.g.
`https://mama7k-api.onrender.com`.

## 3. Web — Render Static Site

*New → Static Site →* the `web` repo.

- **Build command:** `npm ci && npm run build`
- **Publish directory:** `dist`
- **Redirects/Rewrites** (in this order):

  | Source | Destination | Action |
  | --- | --- | --- |
  | `/api/*` | `https://mama7k-api.onrender.com/api/*` | Rewrite |
  | `/*` | `/index.html` | Rewrite |

The first rule makes the API reachable on the web site's own domain; the second lets the client-side router handle
deep links such as `/lineups/…`. Check Render's current docs if the wildcard syntax differs.

## 4. Check after the first deploy

- Open the web site, register a test user, sign out and back in. If sign-in "works" but you are still a guest, the
  cookie is not passing through the rewrite — check that you opened the static site's domain, not the API's.
- **Client IP:** Render → API service → *Logs*. Each request line has `ip=…`; it should be your own public IP (compare
  with any "what is my IP" site). If it shows a private/Render address, raise `TRUSTED_PROXY_HOPS` by one; if it ever
  shows an address you can set yourself with an `X-Forwarded-For` header, lower it. Otherwise everyone shares one
  rate-limit bucket.

## 5. First admin and data

Never run `cmd/seed` against production — it creates test users with the password `password123`.

```bash
DATABASE_URL='<neon string>' go run ./cmd/useradmin -username <you> -name "<display name>" -role SUPER_ADMIN
```

It prints a temporary password; sign in and choose your own password when asked. Then, in **Admin → นำเข้าไฟล์**,
import the five CSVs from `docs/master-data/` (any order) and upload any images still missing from R2.

## 6. After launch

- **Uptime:** a free monitor (e.g. UptimeRobot) on `https://<web site>/api/health` alerts when the API is down. Pinging
  every few minutes also keeps it awake; check the host's current free-tier terms first.
- **Backups:** Neon free keeps only a short restore window. Take regular dumps, e.g.
  `pg_dump '<neon string>' -Fc -f mama7k-$(date +%F).dump`.
- **Forgotten password:** Admin → ผู้ใช้ → รีเซ็ตรหัสผ่าน (Super Admin) gives a one-time temporary password to send to the
  user privately.

## Single-host alternative

On a VPS (or any host that runs one container), the API can serve the web app itself: build the web repo and run the
API with `WEB_DIR` pointing at its `dist/` folder. Same origin, no rewrite needed.
