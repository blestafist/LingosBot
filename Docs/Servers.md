# Linux Servers

Run LingosBot on a Linux server only with a browser that supports headless automation. Firefox or Chrome are the practical choices. Do not use Safari, and do not schedule a visible browser session on a server.

This guide uses a prebuilt Linux release. Replace paths and the `lingosbot` service account with values appropriate for your server.

## 1. Install Dependencies

Install Firefox or Chrome and the standard libraries required by the downloaded release. Selenium Manager normally resolves the matching browser driver automatically on first startup, so the server needs outbound network access at least then.

On Debian or Ubuntu, Firefox is commonly installed with:

```bash
sudo apt update
sudo apt install firefox
```

Package names differ by distribution. Verify the browser can start before configuring the service.

## 2. Create A Dedicated Account And Directory

Use a non-login system account so the bot and its plain-text configuration do not run as `root`.

```bash
sudo useradd --system --create-home --home-dir /opt/lingosbot --shell /usr/sbin/nologin lingosbot
sudo mkdir -p /opt/lingosbot/app /etc/lingosbot
sudo chown -R lingosbot:lingosbot /opt/lingosbot /etc/lingosbot
sudo chmod 700 /opt/lingosbot /opt/lingosbot/app
sudo chmod 700 /etc/lingosbot
```

Download the Linux ZIP release and extract the **entire** archive into `/opt/lingosbot/app`. Keep the bundled `selenium-manager/` directory beside `LingosBot`; it is required for automatic browser-driver management. Do not copy only the executable. Then mark the executable as runnable:

```bash
sudo chmod +x /opt/lingosbot/app/LingosBot
sudo chown -R lingosbot:lingosbot /opt/lingosbot/app
```

Create `/etc/lingosbot/config.json` with headless mode enabled:

```json
{
  "credentials": {
    "email": "you@example.com",
    "password": "replace-with-your-password"
  },
  "browser": "Firefox",
  "headless": true,
  "errorsPer100Words": 0,
  "lessonCount": 1,
  "classLessonCounts": {}
}
```

Protect the configuration file:

```bash
sudo chown lingosbot:lingosbot /etc/lingosbot/config.json
sudo chmod 600 /etc/lingosbot/config.json
```

Test it once before scheduling, as the same account and from the same directory cron will use:

```bash
sudo -u lingosbot sh -c 'cd /opt/lingosbot/app && ./LingosBot --config /etc/lingosbot/config.json'
```

## 3. Schedule Daily Runs With cron

Make sure cron is installed and running on your server (on Debian or Ubuntu, the service is `cron`). The example below runs the bot every day at **18:00 in the server's local time zone**. Change the first two fields for a different minute and hour.

Create a private log file, then edit the `lingosbot` account's crontab:

```bash
sudo -u lingosbot install -m 600 /dev/null /opt/lingosbot/lingosbot.log
sudo crontab -u lingosbot -e
```

Add this single line and save the crontab. The absolute config path avoids cron's working-directory ambiguity. `cd` also gives the bot the same working directory as a manual run. `flock -n` skips an overlapping run if the previous one is still active.

```cron
0 18 * * * umask 077; cd /opt/lingosbot/app && /usr/bin/flock -n /opt/lingosbot/lingosbot.lock /opt/lingosbot/app/LingosBot --config /etc/lingosbot/config.json >> /opt/lingosbot/lingosbot.log 2>&1
```

Check that the entry is installed, and inspect the log after the scheduled time:

```bash
sudo crontab -u lingosbot -l
sudo -u lingosbot tail -n 100 /opt/lingosbot/lingosbot.log
```

A successful manual run does not prove cron is installed or running; check the service and the server's clock if the log stays empty. Cron does not run jobs missed while the server is off. For catch-up runs, use the systemd timer below instead.

## Optional: systemd Timer

Use this instead of cron when you want `journalctl` logs and catch-up runs after downtime. Disable the cron entry first if you already added it, so the bot does not run twice.

Create `/etc/systemd/system/lingosbot.service`:

```ini
[Unit]
Description=LingosBot lesson run
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
User=lingosbot
Group=lingosbot
WorkingDirectory=/opt/lingosbot/app
UMask=0077
ExecStart=/opt/lingosbot/app/LingosBot --config /etc/lingosbot/config.json
```

Create `/etc/systemd/system/lingosbot.timer`:

```ini
[Unit]
Description=Run LingosBot every day at 18:00

[Timer]
OnCalendar=*-*-* 18:00:00
Persistent=true
Unit=lingosbot.service

[Install]
WantedBy=timers.target
```

Reload systemd, test the service, and enable the timer:

```bash
sudo systemctl daemon-reload
sudo systemctl start lingosbot.service
sudo systemctl enable --now lingosbot.timer
sudo systemctl list-timers lingosbot.timer
```

Inspect the latest run and follow logs:

```bash
sudo systemctl status lingosbot.service
sudo journalctl -u lingosbot.service -n 100
sudo journalctl -u lingosbot.service -f
```

`Persistent=true` starts a missed scheduled run after the server returns online. Remove it if a missed lesson must never run late.

## Maintenance

- Update the release by stopping any active run, replacing the complete extracted contents in `/opt/lingosbot/app` including `selenium-manager/`, restoring executable permissions, and testing a manual run before restarting the schedule.
- Keep `config.json`, `diagnostics/`, and the cron log private. They can include account and page data.
- Review `/opt/lingosbot/app/diagnostics/` and `/opt/lingosbot/lingosbot.log` periodically, then delete old files. If you configure log rotation, preserve the `0600` log mode and `lingosbot` ownership.
- Do not run concurrent instances against the same account. The cron example uses a lock to prevent overlap; do not enable both schedulers at once.
