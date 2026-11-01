# Performance checks

## Browser-free API and browser-login handoff

The default `mode: "api"` logs in directly over HTTP using the login form's CSRF
token and an in-memory cookie jar. It does not create a browser. Browser settings,
including `headless` and `useLessonApi`, are ignored in this mode.

With `mode: "browser"`, `headless: false` and `useLessonApi: true`, the bot logs in using the visible
browser, transfers its cookies and user agent to a run-scoped .NET `HttpClient`,
validates the HTTP dashboard, and closes the browser. Cookies are kept only in
memory and updated by the HTTP handler; they are not exported to a file. No
additional `useHttpSession` option is needed. In browser mode, `headless: true` retains
the existing browser workflow. `useLessonApi: false` forces UI only in browser mode.

HTTP mode reads inert `#app[data-props]` JSON from HTML instead of downloading and
executing React, ads, analytics, CSS and fonts. It verifies the selected class
after switching, checks vocabulary completeness, and fetches fresh dashboard
challenge/limit data before every lesson. Challenge joins are confirmed by ID
and `in_progress` status before starting. Unknown challenge statuses or failed
checks stop the run, rather than risking intentional errors during Perfectionism.
Answers use the same sequential lesson state machine, error policy, safety caps,
and Word/Call counters as browser API mode. No POST is replayed on error, no
cross-origin URL/redirect is followed, and HTTP mode never silently falls back
to browser input after starting. Wordset-transition GET redirects are bounded.

Read-only checks on the current Lingos site verified authenticated non-rendered
dashboard HTML, wordset list/detail payloads and client endpoint URLs. No lessons
were started and no answers or challenge/class changes were submitted there.
Unit tests simulate multi-class/multi-lesson runs, fresh Perfectionism state,
joins, limits, incomplete vocabulary, redirects, timeouts, and mode selection.
An additional real Chromium/local HTTP-server smoke test transferred an HttpOnly
login cookie, closed Chromium, then completed a lesson through `HttpSessionRunner`,
including challenge join/confirmation, Perfectionism suppression, identical
prompts, an informational card, and HTTP cookie rotation after browser shutdown.
Actual speed and memory savings on a Celeron have not been measured.

## Direct lesson answers

In browser mode (`mode: "browser"`, `headless: true`, `useLessonApi: true`), after entering a lesson the bot reads its initial
`#app[data-props]` state and sends sequential answers to
`POST /learning/answer/{lessonId}` from the authenticated browser. It follows the
server's returned step rather than waiting for input, feedback, and Continue DOM
updates for every word. Intentional mistakes and their Perfectionism suppression
still use the existing error-rate logic. Unknown exercises and missing vocabulary
stop without guessing. No answers are submitted concurrently.

If the initial payload is unavailable, the existing UI runner is used. After an
API request, errors/timeouts stop the lesson without retry or UI fallback: the
server may already have processed the answer. Set `useLessonApi: false` to force
the old path. Completion reloads the dashboard to refresh challenges and counters.

Unit tests cover API step transitions, rejected alternatives, intentional errors,
new-word cards, completion, caps, malformed responses, and request failures.
A real headless Chromium smoke test against a local HTTP lesson simulator also
passed through the full `LessonRunner`: two identical prompts, intentional errors
and corrections, a new-word card, dashboard refresh, and Perfectionism error
suppression (five submissions normally, three with Perfectionism). No live account
lessons were submitted and no end-to-end speedup is claimed from these tests.
The measurements below predate the API path and concern UI mode only.

## UI mode

The lesson runner reads the prompt, feedback, completion and daily-limit markers
in one synchronous DOM snapshot. It does not cache state across browser commands
or bypass answer validation. Browsers without `Element.checkVisibility` and
failed script reads fall back to Selenium element queries.

Vocabulary collection waits for the first visible row before its existing bulk
read, rather than requesting `Displayed` for every row. The full Selenium scraper
remains available as a fallback.

## Verification

```bash
dotnet test LingosBot.Tests/LingosBot.Tests.csproj
```

Local headless Chromium/Selenium microbenchmarks on the saved `dev_pages` pages
(50 iterations after 5 warmups):

| Operation                           |  Previous | Optimized |
| ----------------------------------- | --------: | --------: |
| Lesson step state read              |  30.17 ms |   1.38 ms |
| Vocabulary readiness check, 84 rows | 424.68 ms |  14.98 ms |

These measure browser-command overhead, not end-to-end lesson speed or Lingos
server latency. Actual performance varies by browser and machine.

Playwright checks covered the saved lesson page plus prompt entry, accepted and
rejected feedback, hidden feedback, hidden duplicate prompts, completion dialogs,
class-selector dialogs, daily limits, and `?finished=1` redirects. No live account
lessons were submitted during these checks.

Full `LessonRunner` smoke tests also passed against a local simulated lesson in
Selenium: two consecutive identical prompts, intentional errors followed by
corrections, and suppression of intentional errors for a perfectionism challenge.
