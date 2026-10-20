# Tolon Poll

A small feedback poll for the Tolon app icons. One Go binary serves the static
pages from `web/` and a JSON API, and it stores votes in SQLite.

## What it serves

The service answers on `tolon.<domain>` through the Cloudflare tunnel. It publishes no ports.

| Path | What | Who |
|------|------|-----|
| `/` | Landing page | Public |
| `/preview/` | The real Tolon UI, built for the browser with a fake Tauri shim (see "Rebuild the preview") | Public |
| `/pool` | The vote form | Public, after an email one-time code and a Turnstile check |
| `/pool/results`, `/api/results` | Results page and JSON | Admin only, through the `tolon_admin` Access app |

Cloudflare Turnstile and Resend are the two external services. The browser loads the
Turnstile widget. The app calls the Turnstile verify endpoint, and it sends code emails
through Resend SMTP.

## Design

- **Voters do not use Cloudflare Access.** The free Zero Trust plan has a 50-seat
  cap. The app verifies each voter's email itself:
  1. `POST /api/otp/request` checks a Turnstile token on the server, then emails a
     6-digit code through Resend SMTP. The database keeps only an HMAC-SHA256 of the code. The code
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

Container limits in `docker-compose.yml`: user `PUID:PGID` (1000), read-only root
filesystem with a `/tmp` tmpfs, `cap_drop: ALL`, `no-new-privileges`, 128 MB memory,
0.5 CPU and 64 pids. The image is about 22 MB. The database directory
`./data/tolon-poll` is a bind mount and is gitignored with the rest of `data/`.

## Privacy

Stored in `poll.db`:

| Table | Contents | Lifetime |
|-------|----------|----------|
| `votes` | Email, favourite, reactions JSON, comment, timestamp | Until you delete it |
| `otp_codes` | HMAC of the email and of the code, expiry, attempt count | Pruned after expiry |
| `otp_sends` | HMAC of the email, send time (for the send limits) | Pruned after 24 h |
| `sessions` | SHA-256 of the session id, email, expiry | Pruned after 24 h |

Not stored: IP addresses, user agents, plain codes, plain session ids. IP addresses
exist only in memory, as keys of the rate limit buckets. The vote email is stored in
plain text, because the admin results page shows it next to the comment. Cloudflare
and Resend keep their own logs under their own policies.

## Setup

1. Terraform (from `terraform/`, see the root README "Running OpenTofu"): apply the
   tunnel config, the DNS records and the `tolon_admin` Access app, then read the AUD with
   `tofu output -raw tolon_admin_aud` into `TOLON_POLL_ACCESS_AUD`.
2. Resend: add the sending domain `tolon.<domain>` in the Resend dashboard, copy the
   records it shows into `resend_dns_records` in `terraform/terraform.tfvars` (full
   record names), apply, and wait until Resend shows the domain as verified. The map has
   a DKIM TXT, an SPF TXT, an MX, the `rsend` tracking CNAME and a `_dmarc` TXT with `p=none`.
3. Create a send-only Resend API key and set it as `SMTP_PASS` in `.env`, with
   `SMTP_HOST=smtp.resend.com`, `SMTP_PORT=465`, `SMTP_USER=resend` and an `SMTP_FROM`
   on the sending domain.
4. Turnstile: see "Enable Turnstile" below.
5. `mkdir -p data/tolon-poll && docker compose up -d --build tolon-poll`.

## Runbook

### Rotate the Resend key

1. Create a new send-only key in the Resend dashboard.
2. Put it in `.env` as `SMTP_PASS`.
3. `docker compose up -d tolon-poll` (this recreates the container with the new value).
4. Request a code on `/pool` to confirm the email arrives, then delete the old key in Resend.

### Enable Turnstile

The Terraform widget is off by default (`enable_tolon_turnstile = false`). The API token
needs the permission "Account > Turnstile > Edit" first. Then:

1. Set `enable_tolon_turnstile = true` in `terraform/terraform.tfvars`.
2. Targeted apply: `-target=cloudflare_turnstile_widget.tolon`.
3. Put `tofu output tolon_turnstile_sitekey` and `tofu output -raw tolon_turnstile_secret`
   into `TURNSTILE_SITEKEY` and `TURNSTILE_SECRET` in `.env`.
4. `docker compose up -d tolon-poll`.

Until the secret is set, the app logs `TURNSTILE_SECRET not set` and rejects every code request.

### Enable the edge rate limit

The ruleset is off by default (`enable_tolon_rate_limit = false`). The API token needs
"Zone > Zone WAF > Edit". Set the variable to `true` and apply with
`-target=cloudflare_ruleset.tolon_rate_limit`. The rule blocks a client for 10 s after
20 requests in 10 s on the poll host. The free plan allows one rate limiting rule.

### Read the results

Open `https://tolon.<domain>/pool/results` and sign in with the admin email OTP.
Without a browser, read the database directly:

```bash
docker run --rm -u "$(id -u):$(id -g)" -v "$PWD/data/tolon-poll":/data keinos/sqlite3 \
  sqlite3 -header /data/poll.db "SELECT favourite, count(*) FROM votes GROUP BY favourite"
```

### Delete one voter's data

Stop the writer first so the delete is not racing a vote, then remove the voter from
every table that holds their email. The code tables are keyed by an HMAC, so they expire by
themselves within 24 h and need no manual step.

```bash
docker compose stop tolon-poll
docker run --rm -u "$(id -u):$(id -g)" -v "$PWD/data/tolon-poll":/data keinos/sqlite3 \
  sqlite3 /data/poll.db "DELETE FROM votes WHERE email = lower('voter@example.com'); DELETE FROM sessions WHERE email = lower('voter@example.com');"
docker compose start tolon-poll
```

The vote is gone, so that email can vote again. To keep them from voting again, leave the row in place.

### Back up the database

The database runs in WAL mode, so do not copy `poll.db` alone. Use SQLite to write a
consistent copy:

```bash
mkdir -p backups
docker run --rm -u "$(id -u):$(id -g)" -v "$PWD/data/tolon-poll":/data -v "$PWD/backups":/out keinos/sqlite3 \
  sqlite3 /data/poll.db "VACUUM INTO '/out/poll-$(date +%F).db'"
```

To restore, stop the service and put the copy at `data/tolon-poll/poll.db`, removing any `poll.db-wal` and `poll.db-shm` files next to it.

### Rebuild the preview

`/preview/` is generated by `apps/tolon-poll/preview-build/build.sh`. It builds the `ui/`
folder of the Tolon repository for the browser, adds a fake Tauri layer and a hand-written
shell, and writes the result to `apps/tolon-poll/web/preview/`.

```bash
apps/tolon-poll/preview-build/build.sh                 # clones the Tolon repo (depth 1)
apps/tolon-poll/preview-build/build.sh ~/src/tolon     # or use a local checkout
```

It needs git, node and npm, and it never modifies the checkout. The service reads `web/`
from disk on each request, so no container restart is needed. Commit the new `web/preview/`.
