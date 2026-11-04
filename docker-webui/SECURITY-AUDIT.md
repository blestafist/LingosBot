# WebUI security audit

**Date:** 2026-11-04. **Status:** three reproduced findings fixed in source; deployment not verified.

## Scope and expected load

Reviewed deployment documentation, Docker/Compose and dependencies, Go routes, authentication, CSRF, sessions, user isolation, SQLite storage, credentials, settings, schedules, library exports and run endpoints. Traced Rust worker configuration, HTTP origin/redirect restrictions, environment filtering and process cancellation. This source review covered security-related frontend rendering and headers; independently reported batch-export and browser checks are recorded below.

Assumed one Linux server, roughly 5–20 users and one worker at a time, behind an HTTPS reverse proxy. Host administrators are trusted; ordinary application users need not be trusted.

## Reproduced findings — now fixed

### 1. High: unused class IDs amplified storage and history responses

- **Issue/impact:** authenticated `POST /api/runs` accepted a 900,000-byte `class_id` for `run` or `classes`, although the worker ignored it. Three synthetic runs persisted 2,700,000 bytes of unused metadata; 50 such records could produce a roughly 45 MB history response. No valid Lingos account was needed to create failed runs.
- **Fix:** `internal/webui/runner.go:55–58` rejects every nonempty ID outside `scrape` in shared `workerArgs`, before database access or process launch. Existing numeric/range checks for scraping remain unchanged.
- **Regression:** `TestWorkerArgs` covers empty, numeric, whitespace and oversized IDs; `TestRunnerRejectsUnusedClassIDBeforeStorage` checks HTTP 400 and no stored runs for an ordinary user.

### 2. High: oversized email reached a vulnerable parser in the Docker build

- **Issue/impact:** the email length check ran after `mail.ParseAddress`. The previous `golang:1.24-alpine` tag resolved to Go 1.24.13, affected by [GO-2026-4986 / CVE-2026-39820](https://pkg.go.dev/vuln/GO-2026-4986). A synthetic address with a 32 KiB comment caused about 565 MB of cumulative allocations, not peak RSS, before rejection. Authenticated credential updates could cause CPU and garbage-collection pressure.
- **Fix:** `internal/webui/auth.go:21–27` enforces the 254-byte normalized email limit before parsing. `Dockerfile:8` uses `golang:1.27.3-alpine`; its manifest was confirmed with HTTP 200 from Docker Registry v2.
- **Regression:** `TestAuthEmailValidation` checks normalization, format rejection, byte boundaries and zero parser allocations for the oversized comment. It also passes on Go 1.24.13, confirming the early guard independently of the compiler upgrade.

### 3. Medium: a slow event reader held the only database connection

- **Issue/impact:** fetching 201 events and returning only 200 left a cursor open during JSON output. In a synthetic loopback TCP reproduction, a non-reading client with a roughly 3.2 MB event page blocked another user's account request until the first connection closed.
- **Fix:** `internal/webui/runner.go:473–477` explicitly closes the event cursor before `writeJSON`, retaining both iteration-error and close-error handling. Pagination and ownership checks are unchanged.
- **Regression:** `TestRunnerEventsReleaseDBBeforeWriting` seeds 201 events, blocks `ResponseWriter.Write` and verifies another query can use the single SQLite connection. The regression checks for all three findings failed before the fixes and pass afterward.

## Checks

- `gofmt`, `go test -race ./...` and `go vet ./...`: passed.
- `govulncheck` v1.9.0, Linux Go 1.27.3: no vulnerable calls or imported packages. It reported the Windows-only module advisory GO-2026-5024 in `golang.org/x/sys`; that package is not imported by this Linux application.
- OSV batch lookup: 163 locked crates.io dependencies, zero advisory matches on the audit date. This checks published dependency advisories, not application logic or container OS packages.
- Independently reported review of POST batch export (`c543d69`): no concrete issues found. Checked all-or-nothing ownership, CSRF/same-origin enforcement, strict bounded JSON, deduplication and the 500-topic limit, SQL placeholders, no distinction leaking foreign versus missing IDs, and GET compatibility; 41 export/library tests passed.
- Independently reported browser checks on frontend `88cb04b`: 15 axe checks, seven viewport widths from 320 to 1440 px, batch Markdown/JSON exports, and run/cancel/error/retry flows passed with a fake worker.
- Dashboard follow-up adds a bounded, validated one-off `lesson_count` and an owner-filtered latest lesson result without changing saved config. Schedule skip uses the expected timestamp, version and lease checks; stale/double submissions cannot silently skip a later date. Synthetic tests cover auth/CSRF, isolation, strict input, persistence, scheduler races and unchanged cron defaults. The final Go race suite contains 162 passing tests; frontend helpers/theme have 19 passing Node tests. This follow-up does not constitute a new dependency or deployment audit.
- MD3/collection/accounts follow-up: 217 Go tests under `-race`, `go vet`, Go build, 267 locked Rust tests, Rust formatting/release build and 20 Node tests passed. `scrape_all` has shared bounded budgets and atomically saves the library/cache only after worker success; failures/cancellation preserve the old data, and absent old classes are intentionally retained. Account deletion checks auth/CSRF, active tasks, the last active administrator, transactional cleanup and non-reused user IDs. Independent source reviews found no confirmed backend bugs. Fake-worker browser checks covered collection/reload without lessons, timezone validation/normalization, credentials verification, removal confirmation/protection, schedule/skip/recovery and MD/JSON exports: 21 axe checks, widths 320–2560 px and 36 mobile keyboard steps passed. Frontend review follow-ups corrected inline error styling/announcement and focus without credentials. Fonts are bundled locally. These checks are not a new dependency/deployment audit or a screen-reader/live-device test.
- Expressive visual follow-up: replaced baseline card outlines/underlined fields with tonal groups, emphasized Google Sans Flex typography and expressive shapes. No backend or application JavaScript changes. Repeated the Go/Node suites and the 21 axe checks with the fake worker; additional browser checks covered native schedule-switch/weekday keyboard operation, reduced motion, forced colors, password-field bounds and both themes across 320–2560 px. Font source provenance, retained glyphs and local-only loading were checked; no runtime dependencies were added. This remains a browser/source follow-up, not a new security audit.
- Telegram follow-up: optional server-side bot token, fixed outbound Bot API origin and private-chat linking; one-use 256-bit codes are stored only as hashes, expire after ten minutes and are revoked on rotation/password reset/disable/unlink. Notifications require explicit owner opt-in and are queued atomically with terminal run transitions. The bot has no lesson-control commands or website-session access. Only administrators can list all bindings or force-disconnect another user; disconnect rechecks the administrator role inside the transaction. Binding metadata is not encrypted, and opt-in run summaries are disclosed to Telegram. Accepted messages cannot be recalled, and an uncertain send may duplicate after restart. This feature adds no dependencies and is not a new deployment/security certification.
- Telegram checks: 262 Go tests passed, including the race suite; `go vet`, build, 20 Node tests and the isolated frontend scenario passed. Fake Bot API tests cover private-message/code boundaries, cursor persistence, identity changes, cancellation, retries and opt-in/ownership cleanup. Chromium checks cover Account linking states, stale GETs and failed saves, all 77 synthetic admin bindings without truncation, confirmed disconnects, both themes at 320 px, forced colors, reduced motion and axe. Real Telegram and production deployment were not exercised; the existing demo was not upgraded during this follow-up.
- No authentication bypass, cross-user disclosure, user-controlled worker SSRF or shell injection was found in the reviewed paths. This is not a security guarantee.

## Limits and deployment

Tests used isolated databases and synthetic credentials/workers. Local databases, secrets and production configuration were not read or modified. Existing history was not rewritten; the ID fix prevents new oversized records. Rebuild and redeploy to apply the source and toolchain fixes.

Docker build/runtime, the actual reverse proxy/TLS deployment and live Lingos behavior were not verified; Docker runtime remained unavailable. Browser checks used a fake worker, not live Lingos. The frontend and legacy desktop were not penetration-tested. Scanners only cover known advisories and supported analysis paths. This automated, source-based audit does not replace a penetration test.
