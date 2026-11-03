# WebUI

A small, multiuser control panel for the Rust worker. The interface is in English. Each user has their own Lingos credentials, saved lesson settings, classes, vocabulary library, schedule and run history. Only administrators can create accounts, reset passwords, change roles or disable users. There is no public registration.

## Local launch (Fish, no Docker)

Requires Go 1.24+ and Rust 1.88+ to build the worker:

```fish
cd /home/pestit/code/LingosBot/worker-server-rust
cargo build --locked --release --bin lingosbot
cd ../docker-webui
set -gx ADMIN_USER admin@example.com
set -gx ADMIN_PASSWORD 'choose-a-long-unique-password'
set -gx ENCRYPTION_KEY 'your-saved-base64-key'
set -gx COOKIE_SECURE 0
set -gx LISTEN 127.0.0.1:8080
set -gx WORKER_BIN /home/pestit/code/LingosBot/worker-server-rust/target/release/lingosbot
go run .
```

Generate the encryption key **once** using `openssl rand -base64 32`, then keep it securely. Reuse the same key after updates and restarts; losing it makes stored Lingos passwords unreadable. The application does not automatically read `.env` outside Compose. Open <http://127.0.0.1:8080>.

`ADMIN_USER` and `ADMIN_PASSWORD` bootstrap the first administrator only, not a permanent backdoor or password-reset mechanism. An existing bootstrap login such as `admin` remains usable; newly created accounts use email addresses. Changing these environment variables does not overwrite an existing account. Password resets belong in the Users section.

`DB_PATH` defaults to `data/webui.db`. Stop the old server before upgrading and back up the **whole data directory and encryption key** while the server is stopped. Migration preserves the previous administrator's encrypted credentials and daily schedule, assigns them to that administrator, and invalidates old sessions. For a legacy upgrade, supply a bootstrap password with at least 12 characters and the original encryption key. Unreadable legacy credentials stop migration without deleting data; restore the correct key or restore a known-good backup before retrying. A database lock rejects a second server using the same file. Do not share a SQLite database over a network filesystem.

## Workflow

- **Lessons:** save the default number of lessons, sync classes and set overrides by class title. `0` skips a class. The same saved settings apply to manual and scheduled runs. Timing and safety limits are under advanced settings.
- **Library:** collect a class's vocabulary, browse topics and words, and download one topic, one class or the entire library as Markdown or JSON. A bundle is one combined document, not a ZIP archive. Collection replaces only that user's previously collected vocabulary for that class, atomically after a successful result. Failed collections leave the previous library intact. JSON preserves the worker's wordset structure; Markdown escapes untrusted text.
- **Runs:** progress is saved on the server, so closing a tab does not stop a task. Reopen it to see the current lesson or a previous run. HTTP operations and timings are under a disclosure, without request bodies, URLs or cookies. Stop cancels future requests; it cannot undo completed lessons or a request already accepted by Lingos.
- **Account:** enter your Lingos **email**, not a username. Passwords are not returned by the API. Saved credentials are not proof that Lingos has accepted them. The application login password and Lingos password are separate.

The UI supports every worker execution mode and every lesson-setting field: `lessonCount`, `classLessonCounts`, `lessonPromptSafetyCap`, `errorsPer100Words`, `answerDelayMinMs`, `answerDelayMaxMs`, `requestTimeoutSeconds`, `maxRunSeconds`. Input limits match the Rust configuration. Each run receives a snapshot via stdin, never passwords in process arguments.

For security, `baseUrl` and `studentDashboardUrl` are managed by the server operator through `WORKER_BASE_URL` (default `https://lingos.pl`) and `WORKER_DASHBOARD_URL` (default `/student/dashboard`). Users cannot point a worker at an internal service or send their credentials to arbitrary hosts. Other worker CLI flags describing input/output transports are controlled by the WebUI rather than exposed as free-form arguments. Worker processes do not inherit server secrets or `LINGOS_*` overrides.

## Scheduling

Choose a time, weekdays and an IANA timezone (for example `Europe/Warsaw`). An optional advanced mode accepts a standard five-field cron expression: **minute hour day-of-month month day-of-week**. All fields are interpreted in the selected timezone, not the browser's or host's implicit timezone. Timezone data is embedded in the Go binary.

The next due time is persisted in SQLite. After downtime, one overdue occurrence is picked up; a backlog of missed lessons is not replayed. If another task is busy, the occurrence remains pending and is retried. Missing credentials or an unavailable worker produce a visible waiting/error state instead of silently consuming the slot. An overdue run may start outside the chosen time as soon as credentials, the account and worker become available. Disable the schedule to discard a pending occurrence. Successful claiming is deduplicated by user and scheduled timestamp. The scheduler uses the same durable run history as manual runs.

The service still needs to be running: an in-process scheduler cannot execute while the machine is off. DST behavior follows the cron parser: nonexistent wall-clock times may be skipped, and repeated wall-clock times can have distinct UTC occurrences. For one occurrence on every date, prefer a time outside the DST transition window or use UTC. Runs interrupted by a restart are marked **interrupted**, not automatically replayed: Lingos may already have accepted some answers. Check their result before retrying.

Only one worker task runs at a time across users. Scheduled tasks wait while it is busy. The operator's `WORKER_MAX_RUN_SECONDS` (default 3600, allowed 1–86400) caps every user's requested duration, including class sync and collection. Linux terminates the worker if the server dies; cancelled runs also terminate the worker's process group. This is a single-server application, not a distributed job queue. Run events retain approximately the latest 5,000 entries per task, the history API shows the latest 50 tasks, and a worker output stream is bounded to 100 MiB (16 MiB per event). Monitor database size and back it up; long-term automatic history deletion is not implemented.

## Security and deployment

- New application passwords require at least 12 characters. Password hashes use salted PBKDF2-SHA256, not reversible encryption. Lingos secrets use AES-GCM bound to the owning user.
- Sessions expire after 12 hours, use random tokens and HttpOnly/SameSite=Strict cookies. Password changes/resets and account disabling revoke sessions. Disabling an account also stops its active worker.
- Unsafe API operations require CSRF protection; read-only GETs never start workers. Login is rate-limited. Ownership is checked on classes, library exports, runs, credentials, settings and schedules. Administrators cannot accidentally remove the last enabled administrator.
- Use `COOKIE_SECURE=1` behind HTTPS. Use `0` only for loopback HTTP during development. Keep the bind address on loopback or a trusted network; never expose local HTTP with passwords to the Internet. The secure cookie setting must match deployment.
- A reverse proxy should enforce HTTPS, request-size/connection limits and access controls. Proxy headers are not trusted for rate limiting: behind a proxy, clients share its IP budget. A shared-IP limit is intentionally conservative; add a trusted-proxy deployment configuration before changing that behavior.
- CSP blocks third-party resources and framing. No CDN fonts/scripts, analytics or telemetry. Do not disable these protections to add a proxy banner or inline script.
- Protect the data directory, encryption key, environment and backups. Email addresses and collected vocabulary are private user data, not encrypted database fields. Local process owners and host administrators remain trusted. MFA, email delivery/recovery and security monitoring are not included.

## Docker Compose

Set `ADMIN_USER`, `ADMIN_PASSWORD`, `ENCRYPTION_KEY` and `COOKIE_SECURE` in the environment or `docker-webui/.env` (never commit it). From the repository root:

```sh
docker compose -f docker-webui/docker-compose.yml up --build
```

For loopback HTTP explicitly set `COOKIE_SECURE=0`; for HTTPS keep `1`. The image runs as an unprivileged user and persists `/data` in a named volume. On an existing root-owned volume, stop the service and change ownership to the image's `webui` user before upgrading; do not make it world-writable.

## Checks

```sh
cd docker-webui
go test -race ./...
go vet ./...
go build ./...
node --check internal/webui/web/static/app.js
```

Test with an isolated database and a fake worker before using real Lingos credentials. Real lesson runs have external side effects and are never retried automatically after an uncertain result.
