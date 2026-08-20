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
dotnet restore
dotnet run
```

On the first run, the bot creates `config.json` in the directory where it was started. It contains all settings, including the login and password. You can also start from `config.example.json`.

`config.json` is ignored by Git because it contains credentials. The password is stored as plain text in this file, so restrict access to it and do not share or commit it.

Set `headless` to `true` in `config.json` to run without a visible browser window, which is useful for servers.

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
