# LingosBot WebUI

Single-admin interface for the Rust worker: manual lesson runs, vocabulary downloads, Lingos credentials, and a daily schedule. Credentials are encrypted at rest and passed to the worker over stdin. This is not a multiuser service: bind to loopback or use a trusted HTTPS proxy, and set `COOKIE_SECURE=1` for HTTPS.

## Run locally without Docker

From Fish, with Go 1.24 or later installed:

```fish
cd /home/pestit/code/LingosBot/docker-webui
set -gx ADMIN_USER admin
set -gx ADMIN_PASSWORD 'choose-a-long-password'
set -gx ENCRYPTION_KEY 'your-saved-base64-key'
set -gx COOKIE_SECURE 0
set -gx LISTEN 127.0.0.1:8080
set -gx WORKER_BIN /home/pestit/code/LingosBot/worker-server-rust/target/release/lingosbot
go run .
```

Generate an encryption key once with `openssl rand -base64 32` and store it securely. Use that same key on every launch: changing it makes saved Lingos credentials unreadable. `DB_PATH` defaults to `data/webui.db`. The UI can be viewed without the worker binary, but worker actions and scheduled runs require it. Open <http://127.0.0.1:8080> and sign in with the configured admin credentials.

## Run with Docker Compose

Set `ADMIN_USER`, `ADMIN_PASSWORD`, and the saved `ENCRYPTION_KEY` in the environment or `docker-webui/.env`, then run from the repository root:

```sh
docker compose -f docker-webui/docker-compose.yml up --build
```

For local HTTP, set `COOKIE_SECURE=0`; for HTTPS use `COOKIE_SECURE=1`. Keep environment secrets private and persist the named volume.

## Schedule

The schedule runs once a day in the server's local timezone (UTC by default in the Docker image). The service must be running at the scheduled time. A run is skipped if another worker is active or credentials are missing; missed runs are not replayed. Scheduled run results are written to the server logs, not the browser activity panel. Manual run settings are separate and are not persisted.

The image builds the Rust worker unchanged. Admin sessions expire after 12 hours. Automatic updates, multiple users, password reset, rate limiting and production multiuser hardening are not included.
