# LingosBot

LingosBot is a .NET console application that automates lessons on [lingos.pl](https://lingos.pl/) through Selenium. It signs in, reads the vocabulary available in the selected class, and completes lessons using those collected translations.

The bot can process every class listed in Lingos, assign a different lesson count to individual classes, choose an available challenge, and optionally make intentional mistakes.

> Browser automation may conflict with lingos.pl terms of service. Use the project only with an account you are allowed to automate and at your own risk.

## Quick Start

Download the ZIP archive for your operating system from [GitHub Releases](https://github.com/blestafist/LingosBot/releases), extract the entire archive, and follow [Docs/Basic.md](Docs/Basic.md). Do not download or move only the `LingosBot` executable: its bundled `selenium-manager` directory must remain beside it. A supported browser and a Lingos account are required; no .NET or Git installation is needed for a prebuilt release.

## How A Run Works

1. The bot signs in and reads the class list from Lingos.
2. For each class, it opens the word sets and builds a translation lookup.
3. It checks whether a challenge is already active; otherwise, it joins the available challenge with the highest point value.
4. It completes the requested lessons for that class.
5. If Lingos reports the daily lesson limit, the remaining lessons for that class are skipped.

When an active challenge is identified as **Perfekcjonizm**, intentional errors are disabled for that lesson. Matching is case-, whitespace-, punctuation-, and Polish-diacritic-tolerant: a `Perfekcjonizm` keyword in the title is sufficient for a title-only/omitted-description variant (including numbered variants); when challenge text includes an explicit error limit, it must be at most one error and include lesson wording. A keyword found only in a description must also have that lesson/max-one-error wording, while negated mentions and explicit maximum-two-or-more conditions are not matched. The configured `errorsPer100Words` rate is used unchanged for all other challenges and normal lessons.

`lessonCount` is the default per-class count, applied independently to every discovered class. `classLessonCounts` contains optional overrides by stable class ID (or change URL for older classes without an ID); set an entry to `0` to skip that class. Classes without an entry still use `lessonCount`, and older title-keyed entries continue to work when the title uniquely identifies a discovered class. See [Docs/Basic.md](Docs/Basic.md#choose-which-classes-to-run).

Run `./LingosBot --run_config` to configure the global lesson count interactively. The first prompt asks whether to configure classes separately; class discovery and per-class prompts run only after answering yes. Blank per-class prompts keep the current setting, while `default`, `fallback`, or `-` removes an override and restores the global fallback. Overrides for classes missing from discovery are retained.

## Common Commands

```bash
# Run a downloaded Linux release.
./LingosBot

# Run a downloaded Windows release in PowerShell.
.\LingosBot.exe

# Run without a visible browser window for one invocation.
./LingosBot --headless

# Discover classes and save them to classLessonCounts.
./LingosBot --scan_classes
```

The exact command-line interface is documented in [Docs/CLI.md](Docs/CLI.md). When running from source, prepend `dotnet run --` to the same arguments.

## Troubleshooting

- **The browser does not start:** verify that the selected browser is installed. If it lives outside the usual location, configure `browserBinaryPath`.
- **Login fails:** check `credentials.email` and `credentials.password` in `config.json`.
- **No classes or vocabulary are found:** Lingos may have changed its markup, or the account may not have access to the expected class and word sets.
- **The bot stops at the daily limit:** this is expected. LingosBot skips the remaining lessons for that class.

## Diagnostics And Privacy

The bot stores diagnostics below `diagnostics/` next to the application executable. Failed runs can write page HTML, a screenshot, the current URL, and exception details. Challenge checks also save the latest dashboard and challenge landing pages during normal runs.

These files may contain account, class, vocabulary, and page-session data. Inspect and redact them before sharing an issue, and delete them when they are no longer needed.

## Documentation

- [Basic setup](Docs/Basic.md): download a release, create `config.json`, and run the bot locally.
- [Command-line interface](Docs/CLI.md): every command-line option and its precedence.
- [Advanced configuration](Docs/Advanced.md): browser paths, timing values, safety caps, and source builds.
- [Linux servers](Docs/Servers.md): headless setup and scheduled runs with systemd timers or cron.

## Development

See [Docs/Advanced.md](Docs/Advanced.md#run-from-source) for the .NET SDK and source-build instructions.

The repository includes focused tests for interactive configuration. `dev_pages/` contains saved Lingos pages used as selector references during development; they are not part of the application at runtime.

## Support

Report bugs and suggestions through the [issue tracker](https://github.com/blestafist/LingosBot/issues).
