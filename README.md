# LingosBot

LingosBot is a .NET console application that automates lessons on [lingos.pl](https://lingos.pl/) through Selenium. It signs in, reads the vocabulary available in the selected class, and completes lessons using those collected translations.

The bot can process every class listed in Lingos, assign a different lesson count to individual classes, choose an available challenge, and optionally make intentional mistakes.

> Browser automation may conflict with lingos.pl terms of service. Use the project only with an account you are allowed to automate and at your own risk.

## Requirements

- .NET SDK 10.0 or newer
- A supported desktop browser: Chrome, Firefox, Edge, or Safari on macOS
- A Lingos account with access to the classes you want to process

Selenium normally obtains a compatible browser driver automatically. Detailed setup instructions for Windows, macOS, and Linux are in [Configuration/Desktop.md](Configuration/Desktop.md).

## Quick Start

```bash
git clone https://github.com/blestafist/LingosBot.git
cd LingosBot
dotnet restore
dotnet run -- --run_config
dotnet run
```

`--run_config` asks for your email, password, browser, headless mode, and error rate, then creates or updates `config.json`. The password is stored in plain text. Keep this file private; it is ignored by Git.

To configure the file manually instead, copy `config.example.json` to `config.json`, add your credentials, set `lessonCount`, and review or remove the example `classLessonCounts` entries.

```bash
# Linux and macOS
cp config.example.json config.json

# Windows PowerShell
Copy-Item config.example.json config.json
```

## How A Run Works

1. The bot signs in and reads the class list from Lingos.
2. For each class, it opens the word sets and builds a translation lookup.
3. It checks whether a challenge is already active; otherwise, it joins the available challenge with the highest point value.
4. It completes the requested lessons for that class.
5. If Lingos reports the daily lesson limit, the remaining lessons for that class are skipped.

`lessonCount` is the default per-class count. `classLessonCounts` can override it by class title; set an entry to `0` to skip that class. See [Configuration/Configuration.md](Configuration/Configuration.md#class-lesson-counts).

## Common Commands

```bash
# Start using config.json in the current directory.
dotnet run

# Show a browser window for this run, regardless of config.json.
dotnet run -- --visible

# Run without a browser window for this run.
dotnet run -- --headless

# Use a different browser for this run without saving it.
dotnet run -- --browser Firefox

# Discover classes and save default counts to classLessonCounts.
dotnet run -- --scan_classes

# Use a configuration file outside the repository.
dotnet run -- --config /path/to/lingosbot.json
```

For all configuration fields and command-line arguments, see [Configuration/Configuration.md](Configuration/Configuration.md).

## Troubleshooting

- **The browser does not start:** verify that the selected browser is installed. If it lives outside the usual location, configure `browserBinaryPath`.
- **Login fails:** check `credentials.email` and `credentials.password` in `config.json`.
- **No classes or vocabulary are found:** Lingos may have changed its markup, or the account may not have access to the expected class and word sets.
- **The bot stops at the daily limit:** this is expected. LingosBot skips the remaining lessons for that class.

## Diagnostics And Privacy

The bot stores diagnostics below `diagnostics/` next to the application executable. Failed runs can write page HTML, a screenshot, the current URL, and exception details. Challenge checks also save the latest dashboard and challenge landing pages during normal runs.

These files may contain account, class, vocabulary, and page-session data. Inspect and redact them before sharing an issue, and delete them when they are no longer needed.

## Documentation

- [Desktop setup](Configuration/Desktop.md): install the SDK and browser on Windows, macOS, or Linux.
- [Configuration reference](Configuration/Configuration.md): `config.json`, class overrides, browser paths, timeouts, and CLI flags.

## Development

```bash
dotnet build
```

The repository currently has no automated test project. `dev_pages/` contains saved Lingos pages used as selector references during development; they are not part of the application at runtime.

## Support

Report bugs and suggestions through the [issue tracker](https://github.com/blestafist/LingosBot/issues).
