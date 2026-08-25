# Desktop Setup

This guide prepares a Windows, macOS, or Linux desktop to run LingosBot locally. You need the .NET SDK and one supported browser. Browser drivers are usually handled automatically by Selenium Manager, so do not download `chromedriver` or `geckodriver` unless automatic setup fails.

## 1. Get The Source Code

Install [Git](https://git-scm.com/downloads) if it is not already available, then clone the project:

```bash
git clone https://github.com/blestafist/LingosBot.git
cd LingosBot
```

You can alternatively download a ZIP archive from GitHub and open a terminal in the extracted `LingosBot` directory.

## 2. Install .NET

The project targets .NET 10. Install the current .NET 10 SDK from [dotnet.microsoft.com/download](https://dotnet.microsoft.com/download).

Verify the installation in a new terminal:

```bash
dotnet --version
```

The result must begin with `10.`. A runtime alone is not enough; install the **SDK**.

### Windows

Download and run the x64 .NET SDK installer. Restart the terminal after setup so that `dotnet` is available on `PATH`.

### macOS

Download the installer matching your Mac: Arm64 for Apple Silicon or x64 for Intel. Safari support requires macOS, but Chrome or Firefox are usually easier to troubleshoot.

### Linux

Install the .NET 10 SDK using the instructions for your distribution on the official .NET download page. On distributions with packaged browsers, install the browser from the system package manager where possible.

## 3. Install A Supported Browser

Set `browser` in `config.json` to one of these values:

| Browser | Windows | macOS | Linux | Headless |
| --- | --- | --- | --- | --- |
| `Chrome` | Yes | Yes | Yes | Yes |
| `Firefox` | Yes | Yes | Yes | Yes |
| `Edge` | Yes | Yes | Yes | Yes |
| `Safari` | No | Yes | No | No |

Chrome, Firefox, and Edge should be installed normally from their official download pages or your operating system's package manager. The bot launches the browser with a separate automation profile; it does not use your normal browser session.

Safari requires extra preparation. In macOS Terminal, enable its bundled WebDriver once:

```bash
safaridriver --enable
```

Safari cannot run headlessly. Use `"headless": false` when `"browser": "Safari"`.

## 4. Restore Dependencies And Configure

From the project directory, restore NuGet packages and open the interactive configuration prompt:

```bash
dotnet restore
dotnet run -- --run_config
```

The prompt creates or updates `config.json`. Enter your Lingos email and password, choose a browser, then accept the other defaults unless you have a reason to change them.

Alternatively, manually create the configuration file:

```bash
# Linux and macOS
cp config.example.json config.json

# Windows PowerShell
Copy-Item config.example.json config.json
```

Then edit `config.json`. Add your credentials, set `lessonCount`, and review or remove the sample `classLessonCounts` entries before the first run. The full field reference is in [Configuration.md](Configuration.md).

## 5. Run And Confirm

For the first run, keep the browser visible so you can see login and lesson navigation:

```bash
dotnet run -- --visible
```

When it works, use the configured mode with:

```bash
dotnet run
```

If the browser executable is installed in a non-standard location, set `browserBinaryPath`; examples are in [Configuration.md](Configuration.md#browser-settings).

## Browser Driver Problems

Selenium Manager downloads or locates a compatible driver automatically during startup. If startup fails:

1. Update the selected browser to its current stable version.
2. Run the bot again with an internet connection so Selenium Manager can resolve the driver.
3. Confirm that `browser` is one of the supported values: `Chrome`, `Firefox`, `Edge`, or `Safari`. Letter case does not matter.
4. Set `browserBinaryPath` if the browser is portable, beta, or installed outside the standard location.
5. For corporate networks, make sure Selenium Manager is allowed to download its driver metadata and binaries.

Do not put a driver executable in the repository or commit a local browser path.
