# Rust Worker: Getting Started

The worker is a small command-line program for Linux. It signs in to your Lingos account and completes your lessons for you, one lesson per class by default. It talks to lingos.pl directly, so no browser is needed.

> [!WARNING]
> Only use an account you are allowed to automate, and follow the lingos.pl rules.

## What You Need

- A Linux computer or server, either a normal PC (x86_64) or an ARM64 one such as a Raspberry Pi. You don't need Rust, .NET or a browser.
- A Lingos account (email and password).
- A terminal. This is the text window where you type commands.

## 1. Install the Worker

1. Make a new folder and open a terminal inside it.
2. Pick a release on the [GitHub Releases](https://github.com/blestafist/LingosBot/releases) page and note its tag, for example `v2.1.0`. Download the `lingosbot-worker-linux-...` file, not the ZIP files (those are for the desktop app).
3. Paste this into the terminal. Change `TAG=` to the tag you picked:

```bash
set -euo pipefail
TAG=v2.1.0  # Replace with the chosen published tag.
case "$(uname -m)" in
    x86_64) ARCH=x64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo 'No prebuilt binary for this architecture' >&2; exit 1 ;;
esac
ASSET="lingosbot-worker-linux-$ARCH"
URL="https://github.com/blestafist/LingosBot/releases/download/$TAG"
curl -fLO "$URL/$ASSET"
curl -fLO "$URL/$ASSET.sha256"
sha256sum -c "$ASSET.sha256"
cp "$ASSET" lingosbot
chmod +x lingosbot
./lingosbot --version
./lingosbot --help
```

The `sha256sum -c` line runs a checksum check, which confirms the file wasn't damaged during the download. If it works, it prints `OK`. The last two lines show the version and help text. They don't connect to Lingos yet.

## 2. Create Your Settings File

The worker only reads a settings file when you point it to one. It never creates or changes the file. In the same folder, create `config.json`. This is [config.example](../../worker-server-rust/config.example) with your login added:

```json
{
  "username": "you@example.org",
  "password": "your-password",
  "lessonCount": 1,
  "errorsPer100Words": 0,
  "answerDelayMinMs": 0,
  "answerDelayMaxMs": 0
}
```

| Field | What to put there |
| --- | --- |
| `username` | Your Lingos login email |
| `password` | Your Lingos password |
| `lessonCount` | Lessons to do in **each** class per run (1–10000). Start with `1` |
| `errorsPer100Words` | Chance (0–100, in %) of answering wrong on purpose, so the results look less perfect. `0` means no mistakes |
| `answerDelayMinMs` / `answerDelayMaxMs` | Pause before each answer, in milliseconds (1000 = 1 second, max 60000). `0` means no pause |

Then make the file readable only by you, because it contains your password:

```bash
chmod 600 config.json
```

## 3. Run It

```bash
./lingosbot --config config.json
```

A run takes at most 15 minutes. When it works, it ends with a line like this:

```text
Lesson completed successfully: 1 lesson(s), 20 word(s), 32.50s.
```

You might see `Daily lesson limit reached; no lessons completed` instead. This isn't an error. Lingos just won't allow more lessons today.

## 4. Run It Every Day

To start the worker automatically, use cron, Linux's built-in scheduler. Run `crontab -e` and add one line. Replace `/home/you/lingosbot` with the full path of your folder:

```text
0 7 * * * cd /home/you/lingosbot && ./lingosbot --config config.json >> worker.log 2>&1
```

This runs the worker every day at 07:00 and saves its output to `worker.log`.

> [!TIP]
> Don't set it up to retry automatically after an error. Lingos may already have accepted part of the work. Check `worker.log` and fix the problem first.

## Common Problems

If something fails, the last line of output starts with an error name, for example `AUTH_FAILED: ...`.

| You see | What it means | What to do |
| --- | --- | --- |
| `sha256sum` prints `FAILED` | The download is damaged | Delete both files and download them again |
| `No prebuilt binary for this architecture` | Your computer type has no ready-made worker | Build it yourself (see the reference) |
| `Permission denied` | The file isn't marked as a program | Run `chmod +x lingosbot` |
| `AUTH_FAILED` (exit code 1) | Wrong login or password, or the session expired | Check `username` and `password` in `config.json`. Try signing in on lingos.pl |
| `NETWORK_ERROR` (exit code 2) | No internet, Lingos didn't answer, or the run took too long | Check your connection and try again later |
| `CONFIG_ERROR` (exit code 3) | A setting is missing or invalid, for example `username is required` | Read the message and fix that field. Check the file for typos, such as a missing comma |
| `PARSING_ERROR` (exit code 3) | Lingos sent something the worker didn't expect | Update to the newest release. If it keeps happening, report it on GitHub |

---

Want every flag, the JSON event protocol, exit codes and internals? → [Technical reference](reference.md)
