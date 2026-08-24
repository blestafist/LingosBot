<div align="center">

# LingosBot 🚀

**Fast automation tool for lingos.pl**  
Selenium + C# · .NET · Cross-platform

[![C#](https://img.shields.io/badge/C%23-239120?style=for-the-badge&logo=c-sharp&logoColor=white)](https://dotnet.microsoft.com/)
[![Selenium](https://img.shields.io/badge/Selenium-43B02A?style=for-the-badge&logo=selenium&logoColor=white)](https://www.selenium.dev/)
[![.NET](https://img.shields.io/badge/.NET-512BD4?style=for-the-badge&logo=dotnet&logoColor=white)](https://dotnet.microsoft.com/)
[![MIT License](https://img.shields.io/badge/License-MIT-yellow?style=for-the-badge)](LICENSE)

</div>

## ✨ Features

- 🔐 Automatic account login
- 📖 Auto-completion of lessons
- 🗃 Support for word databases / dictionaries
- ⚡ High speed (C# + .NET much faster than Python Selenium)
- 🖥️ Works on Windows · Linux · macOS
- 🧩 Easy to extend with new task scenarios
- Headless mode

## 🚀 Quick Start

```bash
git clone https://github.com/blestafist/LingosBot.git
cd LingosBot
cp config.example.json config.json
# Edit config.json: set credentials and lessonCount.
dotnet restore
dotnet run
```

The application is fully non-interactive: every run discovers all classes listed under `Zmień klasę`, switches to each one, and completes `lessonCount` lessons for that class before continuing to the next. `classLessonCounts` can override this number by the exact class name; use `0` to skip a class. If Lingos reports a daily limit for one class, its remaining lessons are skipped and the bot continues with the next class. Login credentials are also read only from this file. If `config.json` is missing, an empty template is created and the process exits with an error until it is configured.

```json
"lessonCount": 1,
"classLessonCounts": {
  "1c 24/25": 0,
  "2AC 2025/2026_Ein tolles Team 2": 2
}
```

The first class is skipped, the second gets two lessons, and every other class gets one. Class-name matching ignores letter case but otherwise uses the name shown under `Zmień klasę`.

`config.json` is ignored by Git because it contains credentials. The password is stored as plain text in this file, so restrict access to it and do not share or commit it.

Set `headless` to `true` in `config.json` to run without a visible browser window, which is useful for servers. A non-zero exit code means that configuration, login, or the bot run failed, so it can be used directly by cron, systemd, or another scheduler.

## Command Line

Use `--config <path>` (or `-c <path>`) with any command to choose a configuration file; otherwise `config.json` in the current directory is used.

```bash
# Scan the classes under "Zmień klasę", print them, and save each to classLessonCounts.
dotnet run -- --scan_classes

# Persist individual settings.
dotnet run -- --set_email user@example.com --set_passwd secret
dotnet run -- --set_headless true --set_browser Firefox
dotnet run -- --set_errors 5 --set_browser_path /usr/bin/firefox

# Configure the same basic settings interactively.
dotnet run -- --run_config

# Override settings for one run without changing the configuration file.
dotnet run -- --headless
dotnet run -- --visible --browser Edge
```

Available arguments: `--scan_classes`/`-s`, `--config`/`-c`, `--set_email`, `--set_passwd`, `--set_headless true|false`, `--headless`/`-h`, `--visible`/`-v`, `--browser`, `--set_browser`, `--set_errors`, `--set_browser_path`, and `--run_config`.

## Browser Support

Set `browser` in `config.json` to `Chrome`, `Firefox`, `Edge`, or `Safari`. Selenium Manager downloads a compatible driver automatically when possible. Use `browserBinaryPath` when the browser executable is installed outside the standard location. Firefox is fully supported in both visible and headless modes; Safari requires macOS and does not support Selenium headless mode.

## ⚠️ Important

Browser automation may violate the terms of service of lingos.pl.  
**Use at your own risk.**

## 📬 Contact

- 💬 **Telegram** → [@qsistch](https://t.me/qsistch)  
- 💻 **GitHub** → [blestafist](https://github.com/blestafist)  
- 📧 **Email** → ufw-public@proton.me

---

⭐️ If this saves you time — give it a star!  
🐛 Found a bug or have an idea? → [Open an issue](https://github.com/blestafist/LingosBot/issues)
