# Configuration Reference

LingosBot reads `config.json` from the current directory by default. Use `--config <path>` or `-c <path>` to select another file.

`config.json` contains your Lingos password in plain text. It is ignored by Git, but keep its file permissions restricted and do not attach it to issues or pull requests.

## Minimal Configuration

```json
{
  "credentials": {
    "email": "you@example.com",
    "password": "replace-with-your-password"
  },
  "browser": "Chrome",
  "headless": false,
  "lessonCount": 1
}
```

All omitted fields use the defaults listed below. `config.example.json` contains a fuller starting point, including sample `classLessonCounts`; review or remove those entries after copying it.

## Settings

| Key | Default | Description |
| --- | --- | --- |
| `baseUrl` | `https://lingos.pl` | Base Lingos URL. Leave unchanged unless the site changes its host. |
| `studentDashboardUrl` | `https://lingos.pl/student-confirmed/group` | Dashboard URL used after login. Normally unchanged. |
| `credentials.email` | Required | Lingos account email. |
| `credentials.password` | Required | Lingos account password. Stored as plain text. |
| `browser` | `Chrome` | `Chrome`, `Firefox`, `Edge`, or `Safari`. Case-insensitive. |
| `browserBinaryPath` | `null` | Absolute path to a browser executable outside the standard install location. |
| `headless` | `false` | `true` hides the browser window. Safari does not support this mode. |
| `errorsPer100Words` | `10` | Chance of deliberately submitting a wrong answer, from `0` to `100`. Set to `0` to disable intentional errors. |
| `lessonCount` | `1` | Default number of lessons to run for every discovered class. Must be at least `1`. |
| `classLessonCounts` | `{}` | Per-class lesson-count overrides. See below. |

## Class Lesson Counts

The bot discovers all available classes through Lingos' **Zmień klasę** selector and applies `lessonCount` to each one. Add `classLessonCounts` to change a particular class:

```json
{
  "lessonCount": 1,
  "classLessonCounts": {
    "1c 24/25": 0,
    "2AC 2025/2026_Ein tolles Team 2": 2
  }
}
```

In this example, `1c 24/25` is skipped, the second class receives two lessons, and all other classes receive one. Names are case-insensitive but otherwise must match the titles shown by Lingos.

Generate the initial object automatically:

```bash
dotnet run -- --scan_classes
```

The command signs in, prints the classes it finds, and writes each one to `classLessonCounts` using the current `lessonCount` as its value. Review the saved file before your next run.

## Browser Settings

Use a standard browser installation when possible. If that is not available, point to the executable with `browserBinaryPath`:

```json
{
  "browser": "Firefox",
  "browserBinaryPath": "/usr/bin/firefox"
}
```

Common examples:

| System | Browser | Example path |
| --- | --- | --- |
| Windows | Chrome | `C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe` |
| Windows | Firefox | `C:\\Program Files\\Mozilla Firefox\\firefox.exe` |
| macOS | Chrome | `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome` |
| macOS | Firefox | `/Applications/Firefox.app/Contents/MacOS/firefox` |
| Linux | Firefox | `/usr/bin/firefox` |
| Linux | Chrome | `/usr/bin/google-chrome` |

Use JSON escaping for Windows backslashes, as shown in the table. Do not set this field merely to select a driver; it is only for the browser executable.

## Advanced Timing Settings

These optional fields are intended for slow machines or unstable networks. Do not add them unless the defaults cause a reproducible problem.

| Key | Default | Unit |
| --- | --- | --- |
| `defaultWaitTimeoutSeconds` | `15` | Seconds |
| `shortWaitTimeoutSeconds` | `4` | Seconds |
| `lessonRestartReuseTimeoutMilliseconds` | `1500` | Milliseconds |
| `pageLoadTimeoutSeconds` | `60` | Seconds |
| `pollingIntervalMilliseconds` | `25` | Milliseconds |
| `lessonPromptSafetyCap` | `30` | Prompts per lesson |

All timing values must be positive. The safety caps stop the bot if the site appears to be looping; they must also be positive.

## Command-Line Arguments

All commands are passed after `--` when using `dotnet run`.

| Argument | Purpose |
| --- | --- |
| `--config <path>`, `-c <path>` | Use a configuration file at `<path>`. |
| `--scan_classes`, `-s` | Discover classes and save `classLessonCounts`. |
| `--run_config` | Configure email, password, browser, headless mode, and error rate interactively. |
| `--headless`, `-h` | Run headlessly once without changing `config.json`. |
| `--visible`, `-v` | Run visibly once without changing `config.json`. Cannot be combined with `--headless`. |
| `--browser <name>` | Use a browser for this run without saving it. |
| `--set_email <email>` | Save the Lingos email. |
| `--set_passwd <password>` | Save the Lingos password. |
| `--set_headless true\|false` | Save the headless setting. |
| `--set_browser <name>` | Save the browser choice. |
| `--set_errors <0-100>` | Save the intentional-error rate. |
| `--set_browser_path <path>` | Save the browser executable path. |

Examples:

```bash
# Save credentials.
dotnet run -- --set_email user@example.com --set_passwd 'correct-horse-battery-staple'

# Save Firefox and run headlessly for this invocation only.
dotnet run -- --set_browser Firefox
dotnet run -- --headless

# Keep an external configuration file outside the repository.
dotnet run -- --config "$HOME/.config/lingosbot/config.json" --run_config
```

Use a shell's quoting rules for passwords that contain spaces or special characters. A command-line password may remain in shell history or process listings; use `--run_config` or edit `config.json` directly when that is a concern.
