# Rust Worker: Technical Reference

New here? Start with [Getting started](README.md).

- [Overview](#overview)
- [Install a Prebuilt Linux Release](#install-a-prebuilt-linux-release)
- [Build and Run](#build-and-run)
- [Settings](#settings)
- [Classes and Vocabulary Collection](#classes-and-vocabulary-collection)
- [JSON, Events and Exit Codes](#json-events-and-exit-codes)
  - [Event Stream](#event-stream)
- [Failure Behavior](#failure-behavior)
- [Development](#development)
  - [Local Measurements](#local-measurements)

## Overview

A standalone CLI for Linux. Sign-in, vocabulary, classes, challenges and lessons all run over HTTP. No browser, .NET, database or Go WebUI is needed. The existing `worker-desktop/` is unchanged.

Use only an account you are allowed to automate, and follow the lingos.pl rules.

## Install a Prebuilt Linux Release

In [GitHub Releases](https://github.com/blestafist/LingosBot/releases), pick a `v<version>` release. Its assets include standalone static musl worker binaries and `.sha256` files, **not ZIPs** (the ZIP archives are for desktop). You need Linux on a matching architecture. Rust, .NET and Selenium are not needed to run it.

In a separate directory, run in Bash (requires `curl` and `sha256sum`):

```bash
set -euo pipefail
TAG=v2.1.0  # Replace with the chosen published tag.
case "$(uname -m)" in
    x86_64) ARCH=x64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo 'No prebuilt binary for this architecture' >&2; exit 1 ;;
esac
ASSET="lingosbot-worker-linux-$ARCH"
URL="https://github.com/blestafist/LingosBot/releases/download/$TAG"
curl -fLO "$URL/$ASSET"
curl -fLO "$URL/$ASSET.sha256"
sha256sum -c "$ASSET.sha256"
cp "$ASSET" lingosbot
chmod +x lingosbot
./lingosbot --version
./lingosbot --help
```

Both downloaded files must sit side by side and keep their original names until the SHA-256 check. The check detects a corrupted download; it does not replace trust in the release source. `--version` and `--help` do not contact Lingos. For an installed release, use `./lingosbot` instead of `./target/release/lingosbot` in the examples below.

## Build and Run

You need **Rust 1.88+** (minimum required by the locked dependencies):

```bash
cd worker-server-rust
cargo build --locked --release
./target/release/lingosbot --version
```

Minimal run:

```bash
./target/release/lingosbot -u 'user@example.org' -p 'password' --json
```

A password in arguments may be visible in the process list and shell history. For regular use, prefer stdin, an environment variable, or a config file with `600` permissions:

```bash
export LINGOS_USERNAME='user@example.org'
read -rsp 'Password: ' LINGOS_PASSWORD; echo
export LINGOS_PASSWORD
./target/release/lingosbot --json
unset LINGOS_PASSWORD
```

The default is **one lesson per discovered class**, with no intentional mistakes and no artificial delays: the next answer is sent right after the previous one is confirmed, as in the C# HTTP version. The request timeout is 30 seconds; the whole run is limited to 900 seconds.

## Settings

The config is read only from an explicitly chosen source. The worker never creates or saves anything. [config.example](../../worker-server-rust/config.example) contains JSON without secrets:

```bash
cp config.example config.json
chmod 600 config.json
./target/release/lingosbot --config config.json --json
```

The sources are mutually exclusive: `--config PATH` (alias `--run-config PATH`), `--config-json JSON` and `--config-json-stdin`. The last one reads a single JSON object from stdin **until EOF**, so the sender must close the stream. Stdin is preferred for passing secrets in the config: unlike `--config-json`, the content does not end up in the process arguments. For example, if `private-config.json` is readable only by its owner:

```bash
chmod 600 private-config.json
./target/release/lingosbot --config-json-stdin --json < private-config.json
```

The config is limited to 1 MiB. `--config`/`--run-config` accept only a regular file. `--config-json '{"lessonCount":2}'` is handy for non-secret settings, but its argument may end up in shell history and the process list. Stdin does not protect the content from the sender or receiver of the stream itself.

Credentials can be given as `username`/`password` or as `credentials.email`/`credentials.password` for compatibility with the C# config. Precedence: CLI → non-empty `LINGOS_USERNAME`/`LINGOS_PASSWORD` variables → top-level JSON fields → `credentials` → defaults. Explicitly empty CLI values are still an error. Reading config files is supported on Unix; on other OSes the path is rejected before any device or pipe is opened.

Main JSON parameters:

| Parameter | Default | Purpose |
| --- | --- | --- |
| `lessonCount` | `1` | Lessons per class, 1–10000 |
| `classLessonCounts` | `{}` | Overrides keyed by `group:ID`; `0` skips the class |
| `errorsPer100Words` | `0` | Probability of an intentional mistake, 0–100%; disabled for Perfekcjonizm |
| `answerDelayMinMs` / `answerDelayMaxMs` | `0` / `0` | Delay range before an answer, 0–60000 ms; `0` disables waiting |
| `lessonPromptSafetyCap` | `30` | Cap on exercises per lesson, 1–10000 |
| `requestTimeoutSeconds` | `30` | Request timeout, 1–300 seconds |
| `maxRunSeconds` | `900` | Overall deadline, including HTTP and delays, 1–86400 seconds |

For example, `"classLessonCounts": {"group:12345": 2, "group:67890": 0}` runs two lessons in the first class and skips the second. Other classes use `lessonCount`. Legacy keys by class name are supported only if the name is unambiguous. The total budget is at most 10000 lessons.

Extra CLI overrides: `--lessons`, `--errors-per100-words` (0–100), `--delay-min-ms`, `--delay-max-ms`, `--timeout-seconds`, `--max-run-seconds`. See `--help` for the full list.

If an old config sets delays, they are kept. To disable them without editing the file, add `--delay-min-ms 0 --delay-max-ms 0`. Speed is bounded by Lingos response latency. Cards are never sent in parallel, and HTTP 429 stops the run without a retry. To add delays, use for example `--delay-min-ms 800 --delay-max-ms 1800`.

For a test server, `baseUrl` and `studentDashboardUrl` are available. The dashboard must be on the same origin. External servers require HTTPS; HTTP is allowed only for loopback. `mode`, if set, must be `api` or empty. Browser settings are ignored; there is no browser fallback.

Parsing details:

- Every key also accepts its snake_case form (`base_url`, `lesson_count`, …).
- `mode` is case-insensitive and may be `null`.
- `classLessonCounts` accepts at most 10000 entries; keys must be unique ignoring case.
- If only the minimum delay is set, the maximum defaults to the same value.
- `lessonPromptSafetyCap` has no CLI flag.

## Classes and Vocabulary Collection

`--scrape-all` fetches a fresh class list and collects all their wordsets **without running lessons**, including empty classes. The final JSON contains `classes` and a `scrapes` array (each element has the single `scrape` format shown below). For an account with no classes, both arrays are empty. On any error, no partial result is returned. Limits for the whole collection: 256 classes, 256 wordsets, 10000 cards and 1 MiB of text. The overall deadline is not reset between classes either.

```bash
./target/release/lingosbot --config-json-stdin --scrape-all < private-config.json
```

This mode enables JSON by itself; `--realtime` adds the event stream. `--scrape-all`, `--scrape-class` and `--list-classes` are mutually exclusive. After collection, the last class may remain selected on Lingos. The WebUI uses this mode for **Collect all words** and saves the library only after a fully successful run.

`--scrape-class GROUP_ID` takes a positive numeric group ID, not a class name or wordset ID. After sign-in, the worker checks that the group belongs to the account, switches to it if needed and confirms the selection with a fresh dashboard, then fetches wordsets and cards sequentially. No lessons run, so the daily lesson limit does not block collection. The selected group on the server may stay changed even on error. The worker creates no files.

```bash
./target/release/lingosbot --config-json-stdin --scrape-class 12345 < private-config.json
```

Collection mode enables JSON by itself. Sample result (values are illustrative):

```json
{"success":true,"scrape":{"class":{"title":"English","group_id":"12345"},"wordsets":[{"info":{"id":"42","name":"Basics","expected_count":1},"entries":[{"word":"house","translation":"дом"}]}]},"api_calls":5,"duration_seconds":1.2}
```

`expected_count` may be `null`; if the server reports a card count, it must match the number received. Class, wordset and challenge IDs in JSON are decimal **strings** so that JavaScript does not lose precision; `active_id` may also be `null`. Duplicate cards are kept during collection. Export limits: up to 256 wordsets, up to 10000 cards per wordset and in total, up to 16 KiB per string and 1 MiB of text overall (including class and wordset names). The HTTP and JSON limits below also apply. Incomplete or inconsistent data causes an error, not a partial result.

`--list-classes` returns the classes and their group IDs from the dashboard after sign-in. It does not run lessons and cannot be combined with `--scrape-class`. This mode enables JSON by itself:

```bash
./target/release/lingosbot --list-classes --config-json-stdin < private-config.json
```

```json
{"success":true,"classes":[{"title":"English","group_id":"12345"}],"api_calls":3,"duration_seconds":0.8}
```

Markdown and JSON export is available in the WebUI. The worker itself returns JSON, not MD.

## JSON, Events and Exit Codes

In a normal run with `--json`, stdout contains exactly one JSON document and no event stream. In scripts, put `--json` first: on a CLI error, only flags parsed before the error are honored. `--help` and `--version` print plain text and do not require sign-in.

Success:

```json
{"success":true,"username":"user@example.org","words_learned":20,"lessons_completed":1,"errors_made":2,"api_calls":27,"classes_limited":0,"classes_empty":0,"challenge_snapshot":{"group_id":"12345","lessons_completed_at_snapshot":0,"available":0,"in_progress":1,"completed":0,"active_id":"42","challenges":[{"id":"42","title":"Trening","description":"Ćwicz","points":5,"status":"in_progress"}]},"duration_seconds":32.5,"message":"Lesson completed successfully"}
```

Error:

```json
{"success":false,"error_code":"AUTH_FAILED","message":"Nieprawidłowy login lub hasło"}
```

| Exit code | `error_code` | Meaning |
| --- | --- | --- |
| `0` | — | Run finished; a daily limit and skipped classes are not errors either |
| `1` | `AUTH_FAILED` | Sign-in failure or expired session |
| `2` | `NETWORK_ERROR` | Network, HTTP error or deadline; also when the result, `--help` or `--version` cannot be written to stdout |
| `3` | `PARSING_ERROR` | Incomplete response, unknown or unconfirmed state |
| `3` | `CONFIG_ERROR` | Invalid arguments or configuration |
| `3` | `LESSON_LIMIT_REACHED` | Daily lesson limit. Normally handled per class as a `class_skipped` event with reason `lesson_limit`, so a run does not exit with it |

`words_learned` is the number of confirmed correct answers to flashcard exercises, not the number of loaded words or new cards shown. `lessons_completed` is completed lessons. `errors_made` is confirmed rejected answers in completed lessons, including intentional ones. `classes_limited` and `classes_empty` count classes that hit the daily limit or have an empty vocabulary. On error, the final JSON contains no partial statistics.

`challenge_snapshot` is the latest dashboard observation for `group_id`, or `null`. `challenges` lists titles, descriptions, points and statuses; `available`, `in_progress` and `completed` are counts per state; `active_id` is the active challenge ID or `null`. This is **not necessarily the final state**: if `lessons_completed_at_snapshot` is less than the final `lessons_completed`, later lessons may have changed it. The WebUI must render untrusted dashboard text safely.

`challenge_snapshots` has the same shape, with one entry per processed class (`challenge_snapshot` is the latest of them). The WebUI reads this list.

Without `--json`, a successful run prints `<message>: N lesson(s), N word(s), X.XXs.` to stdout, and errors go to stderr as `CODE: message`.

`api_calls` counts HTTP requests, not only API calls. It includes sign-in, every request sent while following a redirect, and attempts that ended in a transport error or an error HTTP status. A rejected URL or a deadline that expired before sending is not counted. The counter is in the success result; on error, the final object does not include it.

### Event Stream

`--realtime` enables NDJSON on stdout: one JSON object per line, each line flushed before work continues. It implies JSON and replaces the single-document `--json` mode, even if both flags are given. The last line is the final success or error with `"type":"result"`. A configuration error also produces such a result if the flag was parsed before the CLI error. If the stream cannot be written, the run stops and no POST is retried.

```bash
./target/release/lingosbot --realtime --config-json-stdin < private-config.json
```

Sample lines, not the full log of a specific run:

```json
{"type":"http_request","api_calls":1,"method":"GET","operation":"sign_in","duration_ms":42,"status":200,"error":null}
{"type":"login_success"}
{"type":"class_start","class_title":"English","class_id":"12345","lessons_total":2}
{"type":"lesson_start","class_title":"English","class_id":"12345","lesson":1,"lessons_total":2}
{"type":"lesson_answer","word":"house","correct":true,"intentional":false,"words_learned":1}
{"type":"lesson_complete","class_title":"English","class_id":"12345","lesson":1,"lessons_total":2,"words_learned":1}
{"type":"class_complete","class_title":"English","class_id":"12345","lessons_completed":2}
{"type":"class_skipped","class_title":"German","class_id":"67890","reason":"disabled"}
{"type":"result","success":false,"error_code":"NETWORK_ERROR","message":"HTTP request timed out; not retried."}
```

`http_request` arrives after the headers are received or a transport error occurs, before the body is read. Fields: `type`, `api_calls`, `method`, `operation`, `duration_ms`, `status`, `error`. `api_calls` is the current counter, `status` is the HTTP code or `null`, `error` is `null`, `timeout`, `connection` or `request`. `duration_ms` is a non-negative integer number of milliseconds on a monotonic clock, from building the request to the response headers or error; reading the body and writing the event are not included. Each redirect is counted separately. The event contains no URL/path, query, class/lesson IDs, headers or body.

`operation` is derived only from the in-memory method and path and is always one of these fixed strings:

| `operation` | Action |
| --- | --- |
| `sign_in` | Loading the sign-in form or submitting sign-in |
| `load_dashboard` | Loading the standard dashboard |
| `load_vocabulary` | Loading the wordset list or a wordset's cards |
| `start_lesson` | Lesson start request |
| `submit_answer` | Submitting an answer |
| `continue_lesson` | Moving to the next set in the lesson |
| `select_class` | Switching the selected class (context or legacy group) |
| `join_challenge` | Joining a challenge |
| `load_page` | Any other/unrecognized request, including a non-standard dashboard URL |

Query strings, IDs and other data are not included in these strings. There is no separate HTTP request for finishing a lesson: completion is confirmed by the answer state and the `lesson_complete` event.

`lesson_answer` contains the exercise text `word`, the check result, the intentional-mistake flag and the number of correct answers **in the current lesson**. These words and the export data may be confidential: protect stdout, logs and the stream receiver. Progress may be emitted before a later error and does not mean the lesson finished.

Additional lesson progress events (clients must ignore unknown types):

| `type` | Fields besides `type` |
| --- | --- |
| `class_start` | `class_title`, `class_id`, `lessons_total` |
| `lesson_start` | `class_title`, `class_id`, `lesson`, `lessons_total` |
| `lesson_complete` | `class_title`, `class_id`, `lesson`, `lessons_total`, `words_learned` |
| `class_complete` | `class_title`, `class_id`, `lessons_completed` |
| `class_skipped` | `class_title`, `class_id`, `reason` |

`class_id` is the group ID as a decimal string. `class_title` is untrusted dashboard text; render it as text, not HTML. `lesson` is a 1-based number within the current class; `lessons_total` is the planned number of lessons for the class. `class_start` is emitted before the class is prepared; `lesson_start` before the lesson start request, after the dashboard and challenge checks. `lesson_complete` confirms completion and carries `words_learned` for that lesson only. `class_complete` is emitted only after all planned lessons; `lessons_completed` refers to that class only. `class_skipped.reason` is `disabled` (0 lessons configured, no `class_start`), `empty_vocabulary` or `lesson_limit`. The limit may stop a class after some lessons have completed; then `class_skipped` is emitted, but not `class_complete`. Network/parse errors end with a normal `result`, with no false completion event. Existing event fields are unchanged; new events and fields contain no credentials or answer requests. Lesson progress events are not emitted in class list and export modes.

## Failure Behavior

- One sequential process and one HTTP session; cookies and vocabulary live only in memory. The HTTP client uses connection pooling and keep-alive when the server allows it. Wordset GETs are not run in parallel, to preserve the order of session cookies.
- No POST is retried after a timeout, disconnect, redirect or malformed response: the server may already have processed the request. Do not automatically rerun the whole job after such an error.
- An alternative answer is sent only after the server explicitly rejects the previous one.
- Class switches and challenge joins are confirmed by a fresh dashboard. An incomplete vocabulary or unknown states stop the run.
- A validly empty vocabulary skips only the affected class. The daily limit is also checked per class, since the API does not guarantee it is global.
- An HTTP response is limited to 1 MiB after gzip decompression. HTML is read without building a DOM; scripts, CDATA and templates are not used as page data. Nested SVG/MathML boundaries and their HTML contexts are limited to 64 levels; ordinary HTML nesting needs no stack. JSON rejects duplicate keys, depth over 64 and more than 16384 values.
- Class and wordset discovery is limited to 256 items of each type; a wordset to 10000 cards (HTTP/JSON limits also apply). Cross-origin redirects and system HTTP proxies are disabled.
- The vocabulary is limited to 10000 unique pairs and 1 MiB of stored text, counting normalized keys and the index; a single string is limited to 16 KiB. Exceeding a limit stops the run with `PARSING_ERROR` instead of answering from a partial vocabulary.
- The worker does not write HTML, cookies, passwords or diagnostic dumps to disk. Raw HTTP/JSON parser messages are not printed.

## Development

```bash
cargo fmt --all -- --check
cargo clippy --locked --all-targets -- -D warnings
cargo test --locked
```

Layout, with no empty layers:

```text
src/
  main.rs                 CLI and final output
  config.rs               settings and value validation
  error.rs                stable errors and exit codes
  infrastructure/         HTTP session, cookies, CSRF and HTML
  models.rs               learning API structures
  vocabulary.rs           translation variants and normalization
  lesson.rs               sequential lesson engine
  challenge.rs            challenge states and Perfekcjonizm
  workflow.rs             classes, vocabulary collection and lesson runs
tests/
  unit/                   unit tests for all components
  common/                 local HTTP server for tests
  http.rs                 HTTP session integration tests
  cli.rs                  end-to-end CLI tests
```

All test code lives only in `tests/`. Unit tests from `tests/unit/` are included via `#[cfg(test)]` and `#[path]`: they can test private functions without widening the public API or entering the normal build. Tests need no account, browser or access to lingos.pl.

The release profile uses `opt-level="z"`, LTO, strip and `panic="abort"`. The [shared workflow](../../.github/workflows/ci-release.yml) tests and builds everything (including musl on native x86_64 and aarch64) on every push to any branch and on every PR, but publishes nothing. Each release binary is checked for ELF architecture, absence of `INTERP`/`NEEDED`, running `--version`/`--help`, and SHA-256 via `sha256sum -c`.

A draft release is created only on push of a `v*` tag, if all checks and builds pass and the tag commit is on `main`. The draft description comes from the tag commit message; a human publishes the release. The process is described in [Docs/development/releasing.md](../development/releasing.md).

A local native musl build needs the matching Rust target and `musl-gcc`. Cross-compiling for another architecture also needs a suitable C toolchain/linker; `rustup target add` alone is not enough.

### Local Measurements

Previous version `b67446a`: Linux x86_64, release, one class, 1000 words in the vocabulary and 20 confirmed answers; a full CLI run against a local HTTP server with no intentional delays:

| Build | Binary size, bytes | Peak RSS, KiB |
| --- | --- | --- |
| GNU/glibc | 2 932 584 | 5668 |
| Static musl | 3 044 096 | 4968 |

On this local setup, 20 answers with the old default delays took 26.32 s; with the new settings, about 0.01–0.02 s, with 27 HTTP requests instead of 28. The main gain comes from removing artificial waiting; real speed depends on Lingos response times. This is not a live benchmark of the site and not a measure of Rust outperforming C#.

RSS was measured via `wait4/getrusage` from a small C launcher, to exclude the Python server's memory. This measures the test scenario and is not a guarantee for every vocabulary or TLS connection. Real dashboard/wordset structures were checked read-only; a full live Rust run with sign-in and answers on lingos.pl has not been done yet. The aarch64 build is covered in CI; only x86_64 was verified locally.

After adding the JSON protocol, the local x86_64 release binaries are 2 974 632 bytes (GNU) and 3 089 184 bytes (musl). Time and RSS were not re-measured for the new version.
