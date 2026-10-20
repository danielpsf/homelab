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
| `/api/config` | `{turnstile_sitekey, contact_email, delete_after, processors}`. `delete_after` is an RFC 3339 time or `null`. `processors` is `["cloudflare","resend"]`. | Public |
| `/pool/results`, `/api/results` | Results page and JSON | Admin only, through the `tolon_admin` Access app |

Cloudflare Turnstile and Resend are the two external services. The browser loads the
Turnstile widget. The app calls the Turnstile verify endpoint, and it sends code emails
through Resend SMTP. Cloudflare Email Routing forwards `tolon@<domain>` (the contact
address on the page) to the admin address.

## Languages

The pages are available in nine languages: pt-BR, de, en-US, es, ar, tr, hi, zh-CN and ja.
The page language comes from `?lang=`, then the saved choice (`localStorage`), then the
browser language, then en-US. A flag switcher in the top bar changes it. Arabic is
right to left.

| Path in `web/` | Content |
|----------------|---------|
| `i18n.js` | Picks the language, fills the page, adds the switcher |
| `i18n/<locale>.json` | Page texts and the code email text |
| `options.<locale>.json` | Icon names and descriptions per language. `options.json` stays the list of valid ids. |
| `flags/` | Flag images from flag-icons (MIT, licence in `licenses/`) |
| `fonts/` | Inter, plus Noto Sans Arabic and Noto Sans Devanagari (OFL 1.1, licences in `licenses/`) |

`tools/check-i18n.mjs` checks that every locale has the same keys and placeholders and
that the copy rules hold. It needs node, so run it off the box: `node apps/tolon-poll/tools/check-i18n.mjs`.

The preview at `/preview/` shows the app screens in pt-BR or en-US only, until Tolon
ships its other languages.

## Design

- **Voters do not use Cloudflare Access.** The free Zero Trust plan has a 50-seat
  cap. The app verifies each voter's email itself:
  1. `POST /api/otp/request` checks a Turnstile token on the server, then emails a
     6-digit code through Resend SMTP. The database keeps only an HMAC-SHA256 of the code.
     The code expires after 10 minutes and allows 5 attempts. A new request replaces the old code.
  2. `POST /api/otp/verify` compares the code in constant time. On success it sets
     `__Host-tolon_session`: a random 32-byte id, stored as a SHA-256 hash, valid for
     24 h, with HttpOnly, Secure, SameSite=Strict and Path=/.
  3. A code request answers `202 {"ok":true}` when it is accepted. It also answers
     400 (`invalid` or `turnstile_failed`), 410 (`poll_closed`), 429 (`rate_limited`) and
     503 (`email_unavailable`). None of these answers shows whether an email has already voted.
- **Code email**: the language comes from the `locale` field of the request. The nine
  page languages are supported, and a prefix maps to its language (`pt-*` to pt-BR,
  `en-*` to en-US, `zh-*` to zh-CN, and `de-*`, `es-*`, `ar-*`, `tr-*`, `hi-*`, `ja-*` to
  their base language). Anything else is sent in en-US. The text comes from
  `web/i18n/<locale>.json`. The mail is UTF-8 with an RFC 2047 subject. A failed send is
  logged by SMTP reply code only, never with the server text, which can echo the address.
- **Admin results** (`/pool/results`, `/api/results`) sit behind a path-scoped
  Cloudflare Access app. The app does not trust `Cf-Access-Authenticated-User-Email`.
  It verifies the `Cf-Access-Jwt-Assertion` JWT (or the `CF_Authorization` cookie):
  RS256 against the team certs (cached, refreshed every hour and on an unknown `kid`;
  only one refresh runs at a time), `aud`, `iss`, `exp` and `nbf`. The email claim must
  equal `ADMIN_EMAIL`.
- **One vote per email**: a `UNIQUE` constraint plus a transaction. The favourite
  and the reaction ids are checked against `web/options.json`, which is read again on
  each request. The comment is at most 1000 characters, with control characters removed.
  A vote row holds only email, favourite, reactions JSON, comment and created_at.
  The app stores no IP addresses and no user agents.
- **Retention**: when `POLL_DELETE_AFTER` is set, the poll closes at that time. After it,
  code requests and votes answer `410 poll_closed`. The prune job then deletes all rows in
  `votes`, `sessions`, `otp_codes`, `otp_sends`, `otp_burns` and `otp_lockouts`. It runs at
  startup and every 10 minutes. The live poll closes at the end of 18 January 2027.
- **CSRF**: every POST needs `Content-Type: application/json` and an `Origin`
  equal to `PUBLIC_ORIGIN`.
- **Limits**. Per IP, with token buckets that live in memory: 120 requests/min on the
  whole host, 30/min on `/api/*` and 10 code requests/hour. The key is the
  `CF-Connecting-IP` address: IPv4 per address, IPv6 per /64. Per email, kept in SQLite
  and keyed by an HMAC of the address: 3 codes/hour and 5 codes per 24 h. 3 burnt codes
  (5 wrong attempts each) within 24 h lock the address out for 24 h. Votes allow 5 attempts/hour
  per email. Globally, `CODES_PER_DAY` code emails per 24 h (default 500), with a warning in the log
  at 80 %. Request bodies are capped at 16 KB, and the server has read, write and idle timeouts.
  The edge rule in `terraform/ratelimit.tf` is live: 20 requests per 10 s per IP and
  Cloudflare data centre, then a 10 s block (Cloudflare error 1015).
- **Headers on every response**: a strict CSP. The only external origin is
  `challenges.cloudflare.com`, for Turnstile. The app also sends `nosniff`,
  `Referrer-Policy: no-referrer`, a Permissions-Policy that denies all features, and
  `Strict-Transport-Security: max-age=31536000` without `includeSubDomains`, because
  other subdomains exist.
- **Logs** never contain codes, session ids or full emails. They show only a
  12-character HMAC prefix of the email.

## Configuration

Plain values come from `.env` through `docker-compose.yml`. The three secrets are Docker
secrets and are read from files through `*_FILE` variables. They are not in `.env`.

| Variable | Purpose |
|----------|---------|
| `CF_ACCESS_TEAM_DOMAIN` | `https://<team>.cloudflareaccess.com` (JWT issuer and certs), built from `CF_ACCESS_TEAM` |
| `CF_ACCESS_AUD` | AUD tag of the results Access app (`terraform output -raw tolon_admin_aud`), set as `TOLON_POLL_ACCESS_AUD` |
| `PUBLIC_ORIGIN` | `https://tolon.<domain>`, used for the Origin check and the Turnstile hostname |
| `ADMIN_EMAIL` | the only email that can see the results |
| `TURNSTILE_SITEKEY` | from the Terraform output `tolon_turnstile_sitekey` |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_FROM` | code delivery. Port 465 uses implicit TLS. Other ports must offer STARTTLS. Plaintext is refused. Without these values, `/api/otp/request` answers `503 email_unavailable`. |
| `CONTACT_EMAIL` | contact address on the privacy note (`TOLON_POLL_CONTACT_EMAIL` in `.env`) |
| `POLL_DELETE_AFTER` | RFC 3339 time. After it the poll closes and all personal data is deleted. Empty means no deadline. |
| `CODES_PER_DAY` | global cap of code emails per 24 h (`TOLON_POLL_CODES_PER_DAY` in `.env`, default 500) |

| Docker secret file | Variable | Content |
|--------------------|----------|---------|
| `secrets/tolon_session_secret` | `SESSION_SECRET_FILE` | HMAC key for codes and email hashes, 32 characters or more |
| `secrets/tolon_smtp_pass` | `SMTP_PASS_FILE` | Resend API key, send only |
| `secrets/tolon_turnstile_secret` | `TURNSTILE_SECRET_FILE` | from the Terraform output `tolon_turnstile_secret`. Without it, code requests fail. |

Each file has mode 600 and belongs to uid 65532, the container user.

## Build and test

```bash
docker compose up -d --build tolon-poll
```

The image is a static Go binary (`modernc.org/sqlite`, no cgo) on
`gcr.io/distroless/static-debian12:nonroot`, built in a multi-stage Dockerfile with both
base images pinned by digest. The container runs as uid 65532 (the distroless `nonroot`
user) with a read-only root filesystem and no capabilities. Its only network is `tolon`,
which it shares with `cloudflared` alone. SQLite runs in WAL mode at `/data/poll.db`
(`./data/tolon-poll` on the host).

### Tests

The tests are a [Ginkgo v2](https://onsi.github.io/ginkgo/) suite with
[Gomega](https://onsi.github.io/gomega/) matchers, so each spec reads as a sentence about
behaviour. Run them in a throwaway container from the repository root:

```bash
# full run: random order, race detector (needs cgo, so the Debian golang image)
docker run --rm -v "$PWD/apps/tolon-poll":/src -w /src golang:1.26.10 \
  go run github.com/onsi/ginkgo/v2/ginkgo -r --randomize-all --race --cover

# plain go test works too, for example on the Alpine image
docker run --rm -v "$PWD/apps/tolon-poll":/src -w /src golang:1.26.10-alpine go test ./...
```

| File | What it specifies |
|------|-------------------|
| `tolonpoll_suite_test.go` | Suite bootstrap (`RunSpecs`) |
| `helpers_test.go` | Test harness: an isolated server per spec with its own database, web dir, fake Access team domain (JWKS) and fake Turnstile siteverify |
| `auth_test.go` | Admin results behind Cloudflare Access: JWT checks (table), cookie fallback, `is_admin`, the results payload, single-flight certs refresh |
| `otp_test.go` | One-time codes: requests, Turnstile, limits, lockout, verification and the session cookie, locales and email text, RFC 2047 subjects, SMTP error classes |
| `vote_test.go` | Voting: sessions, one vote per email, validation (table), CSRF (table), logout |
| `server_test.go` | Routes, security headers, `/api/config`, the retention deadline, rate limits and IP keys, the token bucket, secrets, database file modes |
| `hardening_test.go` | Path traversal, SMTP TLS refusal against a fake SMTP server, Access key rotation, `prune`, logs without email addresses |

Every spec builds its own environment in a `BeforeEach` and cleans up with `DeferCleanup`,
so the suite runs in any order. Mail is sent from a goroutine, so specs wait for it with
`Eventually` and check that nothing is sent with `Consistently`. App logs go to
`GinkgoWriter` and show only for a failing spec. Ginkgo and Gomega are test-only
dependencies: the release binary does not link them (`go version -m` lists only the
SQLite modules).

## Container

Settings in `docker-compose.yml`: user 65532 (the distroless `nonroot` user), read-only root
filesystem with a `/tmp` tmpfs, `cap_drop: ALL`, `no-new-privileges`, 128 MB memory,
0.5 CPU and 64 pids. The image is about 22 MB, and its base images are pinned by digest.
The container is on its own network, `tolon`, which only `cloudflared` shares. It is not
on the `tunnel` network and it publishes no ports.

The database directory `./data/tolon-poll` is a bind mount, gitignored with the rest of
`data/`. The app sets the directory to mode 700 and `poll.db`, `poll.db-wal` and
`poll.db-shm` to mode 600 at startup. The host owner of the directory is uid 65532, so
use `sudo` or a throwaway container to read it (see the runbook).

## Privacy

Stored in `poll.db`:

| Table | Contents | Lifetime |
|-------|----------|----------|
| `votes` | Email, favourite, reactions JSON, comment, timestamp | Until `POLL_DELETE_AFTER`, or until you delete it |
| `otp_codes` | HMAC of the email and of the code, expiry, attempt count | Pruned after expiry |
| `otp_sends` | HMAC of the email, send time (for the send limits) | Pruned after 24 h |
| `otp_burns` | HMAC of the email, time a code was burnt | Pruned after 24 h |
| `otp_lockouts` | HMAC of the email, end of the lockout | Pruned after it ends |
| `sessions` | SHA-256 of the session id, email, expiry | Pruned after 24 h |

At `POLL_DELETE_AFTER` every table above is emptied.

Not stored: IP addresses, user agents, plain codes, plain session ids. IP addresses
exist only in memory, as keys of the rate limit buckets. The vote email is stored in
plain text, because the admin results page shows it next to the comment. Cloudflare
(CDN, Turnstile, Email Routing) and Resend process requests and mail under their own
policies. `/api/config` lists them as `processors`.

## Setup

1. Terraform (from `terraform/`, see the root README "Running OpenTofu"): apply the
   tunnel config, the DNS records and the `tolon_admin` Access app, then read the AUD with
   `tofu output -raw tolon_admin_aud` into `TOLON_POLL_ACCESS_AUD`.
2. Resend: add the sending domain `tolon.<domain>` in the Resend dashboard, copy the
   records it shows into `resend_dns_records` in `terraform/terraform.tfvars` (full
   record names), apply, and wait until Resend shows the domain as verified. The map has
   a DKIM TXT, an SPF TXT, an MX, the `rsend` tracking CNAME and a `_dmarc` TXT with `p=none`.
3. Create the three secret files as described in `.env.sample` (mode 600, owner 65532).
   `tolon_smtp_pass` holds a send-only Resend API key. Set `SMTP_HOST=smtp.resend.com`,
   `SMTP_PORT=465`, `SMTP_USER=resend` and an `SMTP_FROM` on the sending domain in `.env`.
4. Turnstile: see "Enable Turnstile" below.
5. Contact address: see "Enable the contact address" below. Set `TOLON_POLL_CONTACT_EMAIL`
   and `POLL_DELETE_AFTER` in `.env`.
6. `mkdir -p data/tolon-poll && docker compose up -d --build tolon-poll`.

## Runbook

### Rotate the Resend key

1. Create a new send-only key in the Resend dashboard.
2. Write it to `secrets/tolon_smtp_pass` (see "Rotate a secret").
3. `docker compose restart tolon-poll`.
4. Request a code on `/pool` to confirm the email arrives, then delete the old key in Resend.

### Rotate a secret

The secret files belong to uid 65532 and have mode 600, so write them from a throwaway
container instead of using `sudo`. The example rotates the Resend key. Use
`tolon_turnstile_secret` or `tolon_session_secret` the same way.

```bash
printf '%s' 'NEW_VALUE' | docker run --rm -i -v "$PWD/secrets:/s" alpine:3.22 \
  sh -c 'cat > /s/tolon_smtp_pass && chmod 600 /s/tolon_smtp_pass && chown 65532:65532 /s/tolon_smtp_pass'
docker compose restart tolon-poll
```

The app reads secrets at startup, so it needs the restart. A new `tolon_session_secret`
makes every open code invalid and resets the per-email send limits and lockouts, because
their keys are HMACs. Stored votes and open sessions are not affected.

### Enable Turnstile

Turnstile is live. To set it up again, the API token needs "Account > Turnstile > Edit"
(shown as Turnstile Sites Write in the token editor). The widget is off by default
(`enable_tolon_turnstile = false`):

1. Set `enable_tolon_turnstile = true` in `terraform/terraform.tfvars`.
2. Targeted apply: `-target=cloudflare_turnstile_widget.tolon`.
3. Put `tofu output -raw tolon_turnstile_secret` into `secrets/tolon_turnstile_secret`
   (see "Rotate a secret") and `tofu output tolon_turnstile_sitekey` into `TURNSTILE_SITEKEY` in `.env`.
4. `docker compose up -d tolon-poll`.

Until the secret is set, the app logs `TURNSTILE_SECRET not set` and rejects every code request.

### Enable the edge rate limit

The rate limit is live. The ruleset is off by default (`enable_tolon_rate_limit = false`).
The API token needs "Zone > Zone WAF > Edit". Set the variable to `true` and apply with
`-target=cloudflare_ruleset.tolon_rate_limit`. The rule blocks a client for 10 s after
20 requests in 10 s on the poll host, per IP and Cloudflare data centre. The client sees
Cloudflare error 1015. The free plan allows one rate limiting rule.

### Enable the contact address

The contact address is live: `tolon@<domain>` forwards to the admin address through
Cloudflare Email Routing. It is off by default (`enable_tolon_contact = false`). The API
token needs "Zone > Email Routing Rules > Edit" and "Account > Email Routing Addresses > Edit".
The token in use also holds "Zone > Zone Settings > Edit". Set the variable to `true` and apply the four
`cloudflare_email_routing_*` resources with `-target`. Cloudflare then adds the MX and SPF
records at the zone apex. The DKIM record for Email Routing is created by Cloudflare and
is not in Terraform. The forwarding address must be verified once from a mail Cloudflare sends to it.

### Read the results

Open `https://tolon.<domain>/pool/results` and sign in with the admin email OTP.
Without a browser, read the database directly:

```bash
docker run --rm -u 65532:65532 -v "$PWD/data/tolon-poll":/data keinos/sqlite3 \
  sqlite3 -header /data/poll.db "SELECT favourite, count(*) FROM votes GROUP BY favourite"
```

### Delete one voter's data

Stop the writer first so the delete is not racing a vote, then remove the voter from
every table that holds their email. The code, send, burn and lockout tables are keyed by an HMAC, so they are pruned
by themselves within 24 h and need no manual step.

```bash
docker compose stop tolon-poll
docker run --rm -u 65532:65532 -v "$PWD/data/tolon-poll":/data keinos/sqlite3 \
  sqlite3 /data/poll.db "DELETE FROM votes WHERE email = lower('voter@example.com'); DELETE FROM sessions WHERE email = lower('voter@example.com');"
docker compose start tolon-poll
```

The vote is gone, so that email can vote again. To keep them from voting again, leave the row in place.

### Back up the database

The database runs in WAL mode, so do not copy `poll.db` alone. Use SQLite to write a
consistent copy:

```bash
docker run --rm -u 65532:65532 -v "$PWD/data/tolon-poll":/data keinos/sqlite3 \
  sqlite3 /data/poll.db "VACUUM INTO '/data/backup-$(date +%F).db'"
```

The copy stays in the data directory, which only uid 65532 can read, so move it somewhere safe
with a throwaway container too. It holds voter emails. To restore, stop the service and put the copy at
`data/tolon-poll/poll.db`, removing any `poll.db-wal` and `poll.db-shm` files next to it.

### Rebuild the preview

`/preview/` is generated by `apps/tolon-poll/preview-build/build.sh`. It builds the `ui/`
folder of the Tolon repository for the browser, adds a fake Tauri layer and a hand-written
shell, and writes the result to `apps/tolon-poll/web/preview/`.

```bash
apps/tolon-poll/preview-build/build.sh                       # pinned commit b178e08
apps/tolon-poll/preview-build/build.sh --rev <commit>        # another Tolon commit
apps/tolon-poll/preview-build/build.sh ~/src/tolon           # a local checkout
```

Usage is `build.sh [--rev <commit>] [path]`. Without a path it clones the Tolon repository
and checks out the commit (default `b178e08`). It needs git, node and npm, and it never
modifies a local checkout. The service reads `web/` from disk on each request, so no
container restart is needed. Commit the new `web/preview/`.
