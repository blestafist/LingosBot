# WebUI

A small, multiuser control panel for the Rust worker. The English Material 3 Expressive-inspired interface uses bundled Google Sans Flex, responsive phone/tablet layouts and light, dark or system appearance. Each user has their own Lingos credentials, saved lesson settings, classes, vocabulary library, schedule and run history. Only administrators can create or remove accounts, reset passwords, change roles or disable users. There is no public registration.

## Local launch (Fish, no Docker)

Use a supported, patched Go release (tested with 1.27.3) and Rust 1.88+ to build the worker:

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

- **Dashboard:** Run now starts a one-off session without saving Config edits or changing cron defaults. Its lesson count is a temporary **default per class**: saved per-class counts still take priority, including `0` to skip. The latest finished lesson run shows saved words, lessons, duration and other available totals; missing reports do not imply zero. Wyzwania are the last observed dashboard of the named class, not live status or a summary of all classes; later lessons may have changed them. The next automatic run and Skip next run are here too.
- **Config:** Lesson defaults contains default lessons, errors per 100 words and lessons per class. Blank class counts use the default; `0` skips that class. Timing, timeouts, safety limits and read-only endpoints are in collapsed Advanced settings. Save config applies these defaults to future runs, including cron. Schedule is on the same page. Old `#lessons` and `#schedule` links still work.
- **Library:** **Collect all words** discovers your current classes and collects their wordsets in one persistent task, without lessons. The library is updated atomically only after a successful result; failure keeps the previous vocabulary. Previously collected classes absent from the new result are retained. Browse wordsets and select the ones to download together. Selections can span classes and remain selected when searching. “Select all shown” adds the current search results; “Clear” removes the entire selection. Each batch accepts up to 500 wordsets and produces **one combined Markdown or JSON file**, not a ZIP. Individual wordset downloads are inside the word preview; class and entire-library downloads remain available in disclosures. Recollection changes wordset IDs: review your selection after refreshing. JSON preserves the worker's wordset structure; Markdown escapes untrusted text.
- **Runs:** activity and history live under a Dashboard disclosure. Progress is saved on the server, so closing a tab does not stop a task. Reopen it to see the current lesson or a previous run. HTTP operations and timings are under a disclosure, without request bodies, URLs or cookies. Stop cancels future requests; it cannot undo completed lessons or a request already accepted by Lingos. Ordinary notifications disappear after six seconds; errors remain visible.
- **Account:** enter your Lingos **email**, not a username. The Lingos password is encrypted on the server and never returned by the API; the email address is not encrypted. **Verify credentials**, in the same credentials card, signs in with the **saved** login and syncs classes, without starting lessons. Saved credentials alone are not proof of a successful sign-in. The verification badge lasts for this page session and resets after replacement/removal or reload; run progress remains saved in history. The application login password and Lingos password are separate.
- **Users:** **Remove user** asks for confirmation before permanently deleting the account and its credentials, config, schedule, library and run history. Stop the user's active task first. The last active administrator cannot be removed. Removed user IDs are never reassigned; stored backups are not erased by this action.

On phones, the main sections stay in the bottom navigation. Appearance is available on the sign-in screen and in the workspace; its non-sensitive preference is saved only in this browser. “System theme” follows OS changes. The [M3 Expressive](https://m3.material.io/blog/building-with-m3-expressive) web styling uses emphasized variable typography, 32/48 px shapes, tonal groups instead of card outlines, a prominent run action, a native-checkbox schedule switch and connected weekday controls. Shape transitions respect reduced motion; high-contrast mode restores native checkboxes. This is lightweight HTML/CSS styling, not an official Material component library. Google Sans Flex is served locally under the bundled SIL Open Font License and trademark notice; there are no external font or script requests or new runtime dependencies.

Batch exports use `POST /api/library/export` with JSON `{"format":"md","topics":[1,2]}` and the session's CSRF token. IDs must belong to the current user. Empty/oversized selections are rejected; any missing or foreign ID rejects the whole download rather than exporting a partial selection. Existing read-only GET exports are unchanged.

`POST /api/runs` accepts optional `lesson_count` (integer 1–10000) only for `mode:"run"`; omission uses saved defaults. `GET /api/runs/latest` returns `{run,result}` for the current user's latest terminal lesson run, even outside the 50-item history window. Either value can be `null`; class sync and collection are excluded.

`POST /api/runs` with `{"mode":"scrape_all"}` runs the worker's `--scrape-all` mode. Existing `mode:"scrape"` with `class_id` still supports single-class API clients. Account deletion uses CSRF-protected `DELETE /api/admin/users/{id}` and returns 409 for an active task or the last active administrator.

The UI supports every lesson-setting field: `lessonCount`, `classLessonCounts`, `lessonPromptSafetyCap`, `errorsPer100Words`, `answerDelayMinMs`, `answerDelayMaxMs`, `requestTimeoutSeconds`, `maxRunSeconds`. Input limits match the Rust configuration. Each run receives a snapshot via stdin, never passwords in process arguments.

For security, `baseUrl` and `studentDashboardUrl` are managed by the server operator through `WORKER_BASE_URL` (default `https://lingos.pl`) and `WORKER_DASHBOARD_URL` (default `/student/dashboard`). Users cannot point a worker at an internal service or send their credentials to arbitrary hosts. Other worker CLI flags describing input/output transports are controlled by the WebUI rather than exposed as free-form arguments. Worker processes do not inherit server secrets or `LINGOS_*` overrides.

## Scheduling

Choose a time, weekdays and an IANA timezone (for example `Europe/Warsaw`, or `UTC`). The timezone field shows inline validation and normalizes recognized names on save; the server independently checks its embedded timezone data. An optional advanced mode accepts a standard five-field cron expression: **minute hour day-of-month month day-of-week**. A custom cron replaces the time and weekdays; **Use time & weekdays** clears it and unlocks those controls. Save schedule to apply the change. All fields are interpreted in the selected timezone, not the browser's or host's implicit timezone. Timezone data is embedded in the Go binary.

The next due time is persisted in SQLite. After downtime, one overdue occurrence is picked up; a backlog of missed lessons is not replayed. If another task is busy, the occurrence remains pending and is retried. Missing credentials or an unavailable worker produce a visible waiting/error state instead of silently consuming the slot. An overdue run may start outside the chosen time as soon as credentials, the account and worker become available. Disable the schedule to discard a pending occurrence. **Skip next run** confirms the exact date, discards just that occurrence (including a pending overdue one), and leaves later runs enabled. It never stops an already started worker. The CSRF-protected `POST /api/schedule/skip` requires `{"nextRun":<displayed Unix timestamp>}`; stale dates, double clicks and already claimed slots return 409 rather than skipping another date. Successful claiming is deduplicated by user and scheduled timestamp. The scheduler uses the same durable run history as manual runs.

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

See [SECURITY-AUDIT.md](SECURITY-AUDIT.md) for the 2026-11-04 source/dependency audit, fixes and deployment limits. This is not a certification or a substitute for testing your reverse proxy and TLS configuration.

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
node --check internal/webui/web/static/theme.js
node --test internal/webui/web/static/*.test.js
```

Test with an isolated database and a fake worker before using real Lingos credentials. Real lesson runs have external side effects and are never retried automatically after an uncertain result.
