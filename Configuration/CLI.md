# Command-Line Interface

Run a downloaded release as `./LingosBot <options>` on Linux or macOS, or `.\LingosBot.exe <options>` in Windows PowerShell. When running from source, use `dotnet run -- <options>`.

The configuration file is `config.json` in the current working directory by default. Set a different file with `--config <path>`.

## Run Options

| Option | Short form | Effect |
| --- | --- | --- |
| `--config <path>` | `-c <path>` | Read and write the selected configuration file instead of `./config.json`. |
| `--scan_classes` | `-s` | Sign in, print discovered classes, and save them to `classLessonCounts`. No lessons run. |
| `--headless` | `-h` | Hide the browser window for this run only. Does not change `config.json`. |
| `--visible` | `-v` | Show the browser window for this run only. Does not change `config.json`. |
| `--browser <name>` | | Use `Chrome`, `Firefox`, `Edge`, or `Safari` for this run only. |

`--headless` and `--visible` cannot be combined. Safari does not support headless mode.

## Save Configuration From The Terminal

These options update the selected `config.json` and exit. They do not start a lesson.

| Option | Accepted value | Saved field |
| --- | --- | --- |
| `--run_config` | None | Opens the interactive basic configuration prompt, including the global lesson count. It first asks whether to configure each class separately; only a `yes` answer signs in and discovers classes for per-class prompts. |
| `--set_email <email>` | Text | `credentials.email` |
| `--set_passwd <password>` | Text | `credentials.password` |
| `--set_headless <true\|false>` | `true` or `false` | `headless` |
| `--set_browser <name>` | Browser name | `browser` |
| `--set_errors <0-100>` | Integer from 0 to 100 | `errorsPer100Words` |
| `--set_browser_path <path>` | Executable path | `browserBinaryPath` |

Examples:

```bash
# Keep the normal configuration outside the release directory.
./LingosBot --config "$HOME/.config/lingosbot/config.json" --run_config

# Save Firefox and a zero intentional-error rate.
./LingosBot --set_browser Firefox --set_errors 0

# Use Firefox visibly one time without changing saved settings.
./LingosBot --browser Firefox --visible
```

Shell command history and process lists can expose values passed to `--set_passwd`. Prefer `--run_config` or manually edit a protected configuration file when that matters.

## Precedence And Combinations

- `--config` applies to all other options in the same invocation.
- `--headless`, `--visible`, and `--browser` override saved values only for that run.
- A configuration-writing option such as `--set_browser` saves and exits before any run-only override is applied.
- `--scan_classes` signs in and updates `classLessonCounts`, then exits.
- Unknown options and missing option values exit with an error.

See [Basic.md](Basic.md) for the normal local workflow and [Advanced.md](Advanced.md) for the JSON configuration reference.
