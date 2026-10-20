# Self-host OpenLoc with Docker

This folder runs OpenLoc and everything it needs on one machine: the app, its
database (Postgres), Redis, file storage (SeaweedFS) and the DOCX/PDF parser.

## What you need

- Docker with Compose v2 (`docker compose version`).
- Port 8080 free, or another port set as `APP_PORT`.
- To build the images yourself: at least 6 GB of memory for Docker. The
  frontend build is killed at 2 GB. On Docker Desktop: Settings → Resources.

Every command below runs from this folder:

```bash
git clone https://github.com/modelfront/openloc.git
cd openloc/deploy
```

## 1. Choose where the images come from

**Build them from this repository** (simplest). Add `--build` to the
`up` command in step 3. Nothing else to set.

**Or use the published images.** Each release publishes
`ghcr.io/modelfront/openloc` and `ghcr.io/modelfront/openloc-parser`, for
amd64 and arm64. The tags are the version (`0.1.0`), the minor version (`0.1`)
and `latest`; from 1.0 on, the major version too (`1`). Set both in `.env`
(step 2), with the version you want:

```bash
OPENLOC_IMAGE=ghcr.io/modelfront/openloc:0.1.0
OPENLOC_PARSER_IMAGE=ghcr.io/modelfront/openloc-parser:0.1.0
```

Then start with `docker compose up -d --no-build` in step 3. Without
`--no-build`, a failed pull falls back to building the images from this
repository.

## 2. Configure

```bash
cp env.example .env
```

Open `.env` and replace every `CHANGE_ME`. Use a new random value for each
secret:

```bash
openssl rand -hex 32
```

`JWT_SECRET` is required: while it is `CHANGE_ME` or shorter than 32
characters, the app refuses to start, and `docker compose ps` shows it as
`restarting`.

Set `BASE_URL` to the address your users open, for example
`https://tms.example.com`. Keep `AI_SECRETS_KEY` safe: if it changes, the
provider keys stored in the app can no longer be read.

If a reverse proxy (Caddy, nginx, Traefik, a load balancer) sits in front of
the app, set `TRUSTED_PROXY_HOPS` to the number of proxies, usually `1`, and
make sure the proxy sends `X-Forwarded-For`. Caddy and Traefik do by default;
nginx needs `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`.
The sign-in rate limits then see each user's own address. The app never trusts
`X-Real-IP` or `True-Client-IP`. Leave the setting at `0` when users reach the
app port directly: the app then ignores every forwarding header, which a user
could otherwise fake. When the setting doesn't match the proxy, every user
shares one limit, and the app logs a warning the first time it sees the
mismatch.

## 3. Start

```bash
docker compose up -d --build     # build from this repository
docker compose up -d --no-build  # or: use the images set in .env
```

First, the `migrate` service sets up the database and exits. Then the app
starts. Check that it is up:

```bash
docker compose ps                # app shows "healthy"
docker compose logs migrate      # ends with "schema is up to date"
```

## 4. Create your organization and its owner

The password is read from `OWNER_PASSWORD` (8 to 72 characters), so it never
shows up in your shell history.

```bash
read -rs OWNER_PASSWORD && export OWNER_PASSWORD
docker compose run --rm -e OWNER_PASSWORD app \
  ./server create-org --name "Acme" --owner-email you@example.com
```

It prints `created organization "Acme" (id …) with owner you@example.com`. Run
it again with another name and email for each further organization.

## 5. Sign in

Open `BASE_URL` (by default http://localhost:8080) and sign in with that email
and password.

## Optional services

- **Spell check:** add `--profile spellcheck` to `up`.
- **A free machine translation engine:** add `--profile mt` to `up`, and set
  `LIBRETRANSLATE_URL=http://libretranslate:5000` in `.env`. `LT_LOAD_ONLY`
  lists the languages it downloads on its first start, which takes a while.

Use the same profiles on every later `up`.

## Send email

Invitations, password resets and notifications are sent by email. Without an
email setup no email is sent, so only the owner from `create-org` can sign in:
nobody can accept an invitation or reset a password.

The app sends through one of two senders: **any SMTP server** (your mail
provider, a sending service, or your own mail server) or **a Google Workspace
mailbox**. Set up one, not both: with both, the app sends no email and says so
in the log.

### Any SMTP server

Your provider's help names the SMTP host, port and login. In `.env`:

```bash
NOTIFICATION_EMAIL=tms@example.com
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=tms@example.com
SMTP_PASSWORD='your-smtp-password'
```

- `NOTIFICATION_EMAIL` is the address the emails come from. Your provider must
  allow sending from it.
- Keep the password in single quotes. Docker Compose reads a `$` in `.env` as
  the start of a variable, also inside double quotes, so a password with `$`
  would arrive changed and the login would fail.
- `SMTP_PORT` is 587 by default. The app then upgrades the connection with
  STARTTLS and stops if the server doesn't offer it. On port 465 the
  connection is encrypted from the start.
- `SMTP_TLS` overrides that choice: `starttls`, `tls` (encrypted from the
  start), or `none`. Use `none` only for a mail relay that needs no login; the
  app refuses to send a password unencrypted, except to `localhost`.
- Leave `SMTP_USERNAME` and `SMTP_PASSWORD` empty for a relay that accepts mail
  without a login: a mail server in your own network, or a provider's relay
  that trusts your server's IP address instead of a password.

The app logs in with a username and password (PLAIN or LOGIN). A mailbox that
allows only OAuth2 for SMTP, as Microsoft 365 is moving to, doesn't work here;
a Microsoft 365 relay connector that allows your server's IP address does.

### Google Workspace

Email goes through a Google Workspace mailbox, sent by a Google Cloud service
account allowed to act as it. This needs Google Workspace, not a personal Gmail
account.

1. In a Google Cloud project, create a service account and add a **JSON key**.
   Note its **client ID** (a number).
2. In the Google Workspace Admin console, open **Security → Access and data
   control → API controls → Manage domain-wide delegation**, and add that
   client ID with the scope `https://mail.google.com/`.
3. Pick the mailbox the emails come from, for example `tms@example.com`.
4. In `.env`, set all three:

   ```bash
   NOTIFICATION_EMAIL=tms@example.com
   NOTIFICATION_SERVICE_CLIENT_ID=123456789012345678901
   NOTIFICATION_SERVICE_PRIVATE_KEY="-----BEGIN PRIVATE KEY-----\nMIIE…\n-----END PRIVATE KEY-----\n"
   ```

   Copy `private_key` from the JSON key file as it is: one line, with `\n`,
   in double quotes.

### Check that email works

1. Run `docker compose up -d`. The log shows which sender the app uses:

   ```bash
   docker compose logs app | grep 📧
   ```

   `SMTP email sender configured (smtp.example.com:587, starttls, from:
   tms@example.com)` or `Gmail API email sender configured (from:
   tms@example.com)` means the settings were read. `No email sender configured`
   or `Email sender init failed` means no email is sent; the second one gives
   the reason.
2. Send a test email: on the sign-in page, click **Forgot password?** and enter
   the owner's email. The server checks the login only now. If the email
   doesn't arrive, the log has the server's answer:

   ```bash
   docker compose logs app | grep 'email to'
   ```

   For example `password-reset email to you@example.com failed: smtp: login:
   535 …` means the server refused the username or password. With Google,
   `gmail: obtain OAuth2 token: …` means: check the client ID, the scope from
   the Google Workspace steps, and that the mailbox exists.

## Everyday tasks

| Task | Command |
|---|---|
| Upgrade (built images) | `git pull`, then `docker compose up -d --build` |
| Upgrade (published images) | change the tags in `.env`, then `docker compose pull` and `docker compose up -d --no-build` |
| App log | `docker compose logs -f app` |
| Stop (data is kept) | `docker compose down` |
| Back up the database | `docker compose exec -T postgres pg_dump -U openloc openloc > backup.sql` |

An upgrade runs `migrate` again before the app starts. **`docker compose down -v`
deletes all data**: the database, the files and Redis.

Going back to an older version can leave long background tasks
(pre-translation, quality estimation, organization deletes) waiting in Redis,
because some older versions do not run that queue. They start again once you
upgrade.

Back up two volumes: `pgdata` (the database) and `seaweedfsdata` (the
uploaded and translated files).

## Good to know

- **Invitation and password-reset links go only by email.** They never appear
  in the app or its log. Without [email](#send-email), keep the owner's
  password safe: it can't be reset.
- **Managing several organizations:** list your email in
  `PLATFORM_ADMIN_EMAILS` and run `up -d` again. The operator console is then
  available to you.
- **Issue reports:** the same `PLATFORM_ADMIN_EMAILS` people receive the bugs,
  enhancements and feature requests users report from the app, by email and
  under **Reported issues**, and answer them there. With the variable empty,
  the app offers no issue reporting.
- **The Playground and guided tours** are on: `DEMO_ORG=true` in `.env`. The
  app builds the Playground, a shared read-only organization with sample
  data, at start and refreshes it daily. To turn it off, set `DEMO_ORG=` and
  run `up -d` again; the app then offers no tours.
- **AI and machine translation providers must be public.** The app refuses to
  call a provider address on a private network (10.x, 172.16–31.x,
  192.168.x, localhost) and does not use `HTTPS_PROXY`. The built-in
  LibreTranslate is the exception.
- **Microsoft sign-in** (`MICROSOFT_CLIENT_ID` and `MICROSOFT_CLIENT_SECRET`):
  in the Entra app registration, open Token configuration and add the
  optional ID-token claims `email` and `xms_edov`. Without them every
  Microsoft sign-in is refused.
- **One copy of each service.** There is no failover.

## Troubleshooting

| You see | Do this |
|---|---|
| The app shows `restarting` in `docker compose ps` | Run `docker compose logs app` and find the error below. |
| `set JWT_SECRET in deploy/.env` (or another name) | That setting is empty in `.env`. |
| `JWT_SECRET must be at least 32 characters` or `still the CHANGE_ME placeholder` | Set `JWT_SECRET` in `.env` to the output of `openssl rand -hex 32`. Everyone signs in again. |
| Microsoft sign-in fails and returns to the login page | Add the `email` and `xms_edov` ID-token claims in the Entra app registration (see "Good to know"). |
| The build stops with `killed` or exit code 137 | Give Docker more memory (see "What you need"). |
| `denied` or `not found` when pulling `ghcr.io/…` | Check that the tag in `.env` exists on `ghcr.io/modelfront/openloc`, or build with `--build`. |
| The app doesn't start | `docker compose logs migrate` and `docker compose logs app`. |
| `create-org` prints nothing | Older Compose versions need `run -T` when the output isn't a terminal. |
| `an account for this email already exists` | Sign in with that account, or pick another owner email. |
| Invitations arrive nowhere | See [Send email](#send-email): the log says whether email is set up. |
