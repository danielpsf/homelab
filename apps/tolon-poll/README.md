# Tolon Poll

A small feedback poll for the Tolon app icons. One Go binary serves the static
pages from `web/` and a JSON API, and it stores votes in SQLite.

## Design

- **Voters do not use Cloudflare Access.** The free Zero Trust plan has a 50-seat
  cap. The app verifies each voter's email itself:
  1. `POST /api/otp/request` checks a Turnstile token on the server, then emails a
     6-digit code. The database keeps only an HMAC-SHA256 of the code. The code
     expires after 10 minutes and allows 5 attempts. A new request replaces the old code.
  2. `POST /api/otp/verify` compares the code in constant time. On success it sets
     `__Host-tolon_session`: a random 32-byte id, stored as a SHA-256 hash, valid for
     24 h, with HttpOnly, Secure, SameSite=Strict and Path=/.
  3. The request always answers `202 {"ok":true}`, so it does not show if an email has voted.
- **Admin results** (`/pool/results`, `/api/results`) sit behind a path-scoped
  Cloudflare Access app. The app does not trust `Cf-Access-Authenticated-User-Email`.
  It verifies the `Cf-Access-Jwt-Assertion` JWT (or the `CF_Authorization` cookie):
  RS256 against the team certs (cached, refreshed every hour and on an unknown `kid`),
  `aud`, `iss`, `exp` and `nbf`. The email claim must equal `ADMIN_EMAIL`.
- **One vote per email**: a `UNIQUE` constraint plus a transaction. The favourite
  and the reaction ids are checked against `web/options.json`, which is read again on
  each request. The comment is at most 1000 characters, with control characters removed.
  A vote row holds only email, favourite, reactions JSON, comment and created_at.
  The app stores no IP addresses and no user agents.
- **CSRF**: every POST needs `Content-Type: application/json` and an `Origin`
  equal to `PUBLIC_ORIGIN`.
- **Limits** (token buckets per `CF-Connecting-IP` unless noted):
  120 requests/min on the whole host, 30/min on `/api/*`, 10 code requests/hour,
  and 5 vote attempts/hour per email. These are persistent in SQLite:
  3 codes/hour per email (keyed by an HMAC of the email) and 200 codes/day in total.
  Request bodies are capped at 16 KB, and the server has read, write and idle timeouts.
  A Cloudflare rate limiting rule (`terraform/ratelimit.tf`) adds an edge limit.
- **Headers on every response**: a strict CSP. The only external origin is
  `challenges.cloudflare.com`, for Turnstile. The app also sends `nosniff`,
  `Referrer-Policy: no-referrer` and a Permissions-Policy that denies all features.
- **Logs** never contain codes, session ids or full emails. They show only a
  12-character HMAC prefix of the email.

## Configuration

These environment variables come from `.env` through `docker-compose.yml`:

| Variable | Purpose |
|----------|---------|
| `CF_ACCESS_TEAM_DOMAIN` | `https://<team>.cloudflareaccess.com` (JWT issuer and certs) |
| `CF_ACCESS_AUD` | AUD tag of the results Access app (`terraform output -raw tolon_admin_aud`) |
| `PUBLIC_ORIGIN` | `https://tolon.<domain>`, used for the Origin check and the Turnstile hostname |
| `ADMIN_EMAIL` | the only email that can see the results |
| `SESSION_SECRET` | HMAC key, 32 characters or more |
| `TURNSTILE_SITEKEY`, `TURNSTILE_SECRET` | from the Terraform outputs. Without the secret, code requests fail. |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS`, `SMTP_FROM` | code delivery. Port 465 uses implicit TLS. Other ports must offer STARTTLS. Plaintext is refused. Without these values, `/api/otp/request` answers `503 email_unavailable`. |

## Build and test

```bash
docker compose up -d --build tolon-poll
docker run --rm -v "$PWD/apps/tolon-poll":/src -w /src golang:1.26.10-alpine go test ./...
```

The image is a static Go binary (`modernc.org/sqlite`, no cgo) on
`gcr.io/distroless/static-debian12:nonroot`. The container runs as `PUID:PGID` with a
read-only root filesystem and no capabilities. Its only network is `tunnel`. SQLite
runs in WAL mode at `/data/poll.db` (`./data/tolon-poll` on the host).
