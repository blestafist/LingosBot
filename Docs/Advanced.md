# Advanced Configuration

This page documents optional fields in `config.json`. Start with [Basic.md](Basic.md); most users only need credentials, `errorsPer100Words`, and `lessonCount`. The default mode is browser-free API.

This page covers the legacy .NET application in `worker-desktop/`. For the independent Rust HTTP worker, including binary installation and source builds, see its [README](../worker-server-rust/README.md); the CI and release workflow is [ci-release.yml](../.github/workflows/ci-release.yml).

## Full Setting Reference

| Key | Default | Notes |
| --- | --- | --- |
| `baseUrl` | `https://lingos.pl` | Leave unchanged unless Lingos changes its host. |
| `studentDashboardUrl` | `https://lingos.pl/student/dashboard` | Leave unchanged unless Lingos changes its dashboard URL. |
| `credentials.email` | Required | Lingos account email. |
| `credentials.password` | Required | Lingos account password in plain text. |
| `mode` | `api` | `api`: HTTP login and lessons, no browser. `browser`: enables Selenium and browser settings. Missing/null/blank also means `api`. |
| `browser` | `Chrome` | Browser mode only: `Chrome`, `Firefox`, `Edge`, or `Safari`; case-insensitive. |
| `browserBinaryPath` | `null` | Browser mode only: absolute path to a non-standard browser executable. |
| `headless` | `false` | Browser mode only. `false`: visible login, then HTTP handoff. `true`: keep the headless browser workflow. Unsupported by Safari. With `useLessonApi: false`, keep the browser and use UI input/clicks. |
| `useLessonApi` | `true` | Browser mode only: set `false` to force UI. Headless browser mode can use UI if initial lesson data is missing. API mode always uses HTTP and stops on unrecognized data; no browser fallback. Failed API submissions are never automatically retried. |
| `errorsPer100Words` | `10` | Intentional wrong answers from `0` to `100`. |
| `lessonCount` | `1` | Default number of lessons for every discovered class. Must be at least `1`. |
| `classLessonCounts` | `{}` | Lesson-count overrides by stable class key (`group:<GroupId>` or `url:<change URL>`). A value of `0` skips a class. |
| `defaultWaitTimeoutSeconds` | `15` | Standard Selenium wait (browser mode only). |
| `shortWaitTimeoutSeconds` | `4` | Short Selenium wait timeout. |
| `lessonRestartReuseTimeoutMilliseconds` | `1500` | Delay used while reusing the lesson entry page. |
| `pageLoadTimeoutSeconds` | `60` | Browser page-load timeout. |
| `pollingIntervalMilliseconds` | `25` | Selenium polling interval. |
| `lessonPromptSafetyCap` | `30` | Maximum prompts allowed in one lesson before stopping. API mode also limits total submissions to four times this value and wordset transitions to this value. |
| `challengeLessonSafetyCap` | `40` | Reserved configuration field. It is accepted and validated but is not currently enforced by the application. Do not rely on it to limit challenge behavior. |

Browser timing fields are ignored in API mode; in browser mode they must be positive. `lessonPromptSafetyCap` must always be positive. Only increase timeouts after observing a reproducible timeout on a slow connection or machine.

Plain HTTP login and session requests use an independent 30-second timeout (also after a visible browser login). `defaultWaitTimeoutSeconds` does not change it. Ambiguous failed submissions are not retried.

Migration: use `mode: "browser", "headless": true` instead of the removed `browser-headless` mode. Old configurations without `mode` now select API even if they contain `headless: true`. Explicitly set `mode: "browser"` to retain browser behavior.

`--run_config` asks for `lessonCount` and, only when the first prompt is answered yes, signs in to discover classes and asks for a count for each class. Answering no preserves existing `classLessonCounts` overrides while changing the global default. When configuring classes separately, a blank class prompt keeps the current setting (or uses the global fallback for a new class); enter `default`, `fallback`, or `-` to remove an override. The fallback is not written as a separate override for every class, and overrides for classes missing from discovery are preserved.

## Non-Standard Browser Locations

Set `browserBinaryPath` only when the browser is portable, beta-channel, or installed outside the normal location. It is the path to the browser itself, not the Selenium driver.

```json
{
  "mode": "browser",
  "browser": "Firefox",
  "browserBinaryPath": "/usr/bin/firefox"
}
```

| System | Browser | Example path |
| --- | --- | --- |
| Windows | Chrome | `C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe` |
| Windows | Firefox | `C:\\Program Files\\Mozilla Firefox\\firefox.exe` |
| macOS | Chrome | `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome` |
| macOS | Firefox | `/Applications/Firefox.app/Contents/MacOS/firefox` |
| Linux | Firefox | `/usr/bin/firefox` |
| Linux | Chrome | `/usr/bin/google-chrome` |

Windows paths in JSON need doubled backslashes.

## Separate Configuration Files

Use `--config` to keep credentials outside the extracted release directory or to maintain separate accounts:

```bash
./LingosBot --config "$HOME/.config/lingosbot/config.json"
```

The parent directory is created automatically when a command saves the file. The process must have permission to read and write it.

## Diagnostics

Diagnostics are stored in a `diagnostics/` directory next to the executable. A failed browser run can write page HTML, screenshots, URLs, and exception details. Browser challenge checks also save dashboard snapshots during normal runs. After switching to HTTP-only mode there is no live browser to screenshot, and HTTP pages/cookies are not written to diagnostics.

These files may contain account, class, vocabulary, and page-session data. Do not publish them unchanged. Delete old diagnostics when they are no longer useful.

## Run From Source

Use a source checkout only if you want to develop or modify LingosBot. Install the .NET 10 SDK and Git (a browser is needed only for browser mode), then run:

```bash
git clone https://github.com/blestafist/LingosBot.git
cd LingosBot/worker-desktop
dotnet restore
dotnet build
dotnet run -- --run_config
dotnet run
```

Source users can use the same options documented in [CLI.md](CLI.md).
