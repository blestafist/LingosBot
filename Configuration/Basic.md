# Basic Setup

This guide is for running a ready-made LingosBot release on a personal computer. You do not need Git, an IDE, or the .NET SDK.

## 1. Download A Release

Open [GitHub Releases](https://github.com/blestafist/LingosBot/releases), download the ZIP archive matching your operating system, and extract the **entire** archive to a private directory. Do not extract it to a shared downloads folder because `config.json` will contain your Lingos password.

The extracted directory contains the LingosBot executable and its Selenium Manager helper. Keep this layout intact:

```text
LingosBot-release/
├── LingosBot                 # LingosBot.exe on Windows
└── selenium-manager/
    ├── linux/selenium-manager
    ├── macos/selenium-manager
    └── windows/selenium-manager.exe
```

Selenium Manager locates or downloads a compatible browser driver. Do not download it separately, delete it, or move only `LingosBot` out of the extracted directory; the browser may then fail to start.

LingosBot reads `config.json` from the **current working directory**, not automatically from the executable directory. The commands below assume your terminal is open in the extracted directory. Use `--config <path>` to keep the file elsewhere.

LingosBot reads `config.json` from the **current working directory**, not automatically from the executable directory. The commands below assume your terminal is open in the extracted directory. Use `--config <path>` to keep the file elsewhere.

LingosBot reads `config.json` from the **current working directory**, not automatically from the executable directory. The commands below assume your terminal is open in the extracted directory. Use `--config <path>` to keep the file elsewhere.

## 2. Install A Browser

Install one supported browser if it is not already installed:

- Chrome
- Firefox
- Edge
- Safari on macOS only

Chrome, Firefox, and Edge work in visible and headless modes. Safari works only on macOS and cannot run headlessly. The bundled Selenium Manager normally finds or downloads a compatible driver automatically; you usually do not need to install a driver yourself.

## 3. Create `config.json`

Start the program once with interactive setup.

**Windows PowerShell**

```powershell
.\LingosBot.exe --run_config
```

**macOS or Linux**

```bash
./LingosBot --run_config
```

The prompt asks for:

- Lingos email and password
- browser name
- whether to hide the browser window
- number of deliberate mistakes per 100 answers

It creates or updates `config.json` in the current directory. Your password is stored there as plain text. Do not send or commit this file.

On macOS or Linux, restrict the file immediately after setup:

```bash
chmod 600 config.json
```

On Windows, keep the extracted directory in your personal profile and do not grant other local accounts access to it.

If the file does not exist, a normal run also creates a blank template, but then exits because credentials are missing. `--run_config` is the easier first step.

## 4. Run The Bot

For the first run, use a visible browser and confirm that Lingos opens correctly.

**Windows PowerShell**

```powershell
.\LingosBot.exe --visible
```

**macOS or Linux**

```bash
./LingosBot --visible
```

Later, run the executable without options to use the values saved in `config.json`.

## Choose Which Classes To Run

By default, LingosBot processes every class visible in Lingos and runs `lessonCount` lessons in each. You can set the total number of lessons and per-class counts interactively with `--run_config`. The first prompt asks whether to configure classes separately. Answer `no` to set only the global `lessonCount` (and leave any existing `classLessonCounts` overrides unchanged), or answer `yes` to sign in, discover the classes, and enter a count for each one. Use `0` for a class to skip it.

The noninteractive class-discovery command remains available when you want to edit counts manually. First discover the exact class titles:

```bash
./LingosBot --scan_classes
```

This writes a `classLessonCounts` object to `config.json`. Edit its values to choose the number of lessons per class. `0` skips a class.

```json
{
  "lessonCount": 1,
  "classLessonCounts": {
    "Class to skip": 0,
    "Class needing two lessons": 2
  }
}
```

Names are case-insensitive but otherwise need to match Lingos. Classes not listed in `classLessonCounts` use `lessonCount`.

## Basic Configuration Example

```json
{
  "credentials": {
    "email": "you@example.com",
    "password": "replace-with-your-password"
  },
  "browser": "Chrome",
  "headless": false,
  "errorsPer100Words": 10,
  "lessonCount": 1,
  "classLessonCounts": {}
}
```

Use `0` for `errorsPer100Words` if the bot should never make intentional mistakes. See [Advanced.md](Advanced.md) for every optional setting and [CLI.md](CLI.md) for one-time command overrides.

## Common Problems

- **The executable does not start on macOS or Linux:** make it executable with `chmod +x LingosBot` and run it again.
- **The browser does not start:** verify that the `selenium-manager/` directory is still beside the executable, install or update the selected browser, then retry with an internet connection so Selenium Manager can resolve the driver.
- **Login fails:** re-run `--run_config` or correct the credentials in `config.json`.
- **The daily lesson limit is reached:** this is normal. LingosBot skips the remaining lessons for that class.
