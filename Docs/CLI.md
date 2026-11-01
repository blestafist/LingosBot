# Command-Line Interface

Run a downloaded release as `./LingosBot <options>` on Linux or macOS, or `.\LingosBot.exe <options>` in Windows PowerShell. When running from source, use `dotnet run -- <options>`.

The configuration file is `config.json` in the current working directory by default. Set a different file with `--config <path>`.

## Run Options

| Option | Short form | Effect |
| --- | --- | --- |
| `--config <path>` | `-c <path>` | Read and write the selected configuration file instead of `./config.json`. |
| `--scan_classes` | `-s` | Sign in, print discovered classes, and save them to `classLessonCounts`. No lessons run. |
| `--mode <api\|browser>` | | Override the saved mode for this run. Default mode is `api`. |
| `--headless` | `-h` | Set `headless: true` for this run; only affects `mode: "browser"`. Does not select browser mode. |
| `--visible` | `-v` | Set `headless: false` for this run; only affects `mode: "browser"`. With `useLessonApi: true`, the visible browser closes after login and hands off to HTTP. |
| `--browser <name>` | | Use `Chrome`, `Firefox`, `Edge`, or `Safari` for this run only in browser mode. Ignored in API mode. |

Run-only overrides do not change `config.json` during a normal run. `--headless` and `--visible` cannot be combined. Safari does not support headless mode. In API mode no browser is launched, even with `--headless`, `--visible` or `useLessonApi: false`.

## Save Configuration From The Terminal

These options update the selected `config.json` and exit. They do not start a lesson.

| Option | Accepted value | Saved field |
| --- | --- | --- |
| `--run_config` | None | Opens the interactive basic configuration prompt, including the global per-class fallback. It first asks whether to configure each class separately; only a `yes` answer signs in and discovers classes for optional per-class overrides. Blank prompts keep current settings; `default`, `fallback`, or `-` removes a class override. Entries for classes missing from discovery are preserved. |
| `--set_email <email>` | Text | `credentials.email` |
| `--set_passwd <password>` | Text | `credentials.password` |
| `--set_headless <true\|false>` | `true` or `false` | `headless` |
| `--set_mode <api\|browser>` | `api` or `browser` | `mode` |
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
./LingosBot --mode browser --browser Firefox --visible

# Save the browser workflow without a visible window.
./LingosBot --set_mode browser --set_headless true

# Return to the browser-free default.
./LingosBot --set_mode api
```

Shell command history and process lists can expose values passed to `--set_passwd`. Prefer `--run_config` or manually edit a protected configuration file when that matters.

## Precedence And Combinations

- `--config` applies to all other options in the same invocation.
- `--headless`, `--visible`, and `--browser` override saved values only for that run.
- `--mode` independently selects the workflow. Headless/visible flags never change it. `--set_mode` and `--set_headless` can be saved together.
- Missing, null or blank `mode` means `api`. Migrate `browser-headless` to `mode: "browser"` plus `headless: true`; the old mode name is rejected with a migration hint.
- A configuration-writing option such as `--set_browser` saves and exits before any run-only override is applied.
- `--scan_classes` signs in using the selected mode and saves the effective configuration, including any run overrides, together with `classLessonCounts`, then exits.
- Unknown options and missing option values exit with an error.

See [Basic.md](Basic.md) for the normal local workflow and [Advanced.md](Advanced.md) for the JSON configuration reference.
