# Performance checks

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

| Operation | Previous | Optimized |
| --- | ---: | ---: |
| Lesson step state read | 30.17 ms | 1.38 ms |
| Vocabulary readiness check, 84 rows | 424.68 ms | 14.98 ms |

These measure browser-command overhead, not end-to-end lesson speed or Lingos
server latency. Actual performance varies by browser and machine.

Playwright checks covered the saved lesson page plus prompt entry, accepted and
rejected feedback, hidden feedback, hidden duplicate prompts, completion dialogs,
class-selector dialogs, daily limits, and `?finished=1` redirects. No live account
lessons were submitted during these checks.

Full `LessonRunner` smoke tests also passed against a local simulated lesson in
Selenium: two consecutive identical prompts, intentional errors followed by
corrections, and suppression of intentional errors for a perfectionism challenge.
