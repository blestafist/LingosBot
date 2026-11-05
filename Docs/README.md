# LingosBot Documentation

Every component has two levels of docs:

- 🟢 **Getting started**: plain steps, no prior knowledge needed.
- 🔧 **Technical reference**: every flag, environment variable, protocol detail and internal.

## Pick your component

| | 🟢 Getting started | 🔧 Technical reference |
| --- | --- | --- |
| 🌐 **WebUI** (Go): web panel, schedules, Telegram | [Use or host the WebUI](webui/README.md) | [Configuration and internals](webui/reference.md) · [Security and deployment limits](webui/security.md) |
| ⚡ **Rust worker**: headless Linux CLI | [Install and run](worker/README.md) | [CLI, JSON events, exit codes](worker/reference.md) |
| 🖥 **Desktop app** (.NET, legacy) | [Overview](desktop/README.md) · [Basic setup](desktop/basic.md) | [CLI](desktop/cli.md) · [Advanced configuration](desktop/advanced.md) · [Linux servers](desktop/servers.md) · [Performance](desktop/performance.md) |

Not sure which one you need?

- **You want a website where you click "Run":** use the WebUI. If someone already hosts it for you, you only need a browser.
- **You have a Linux server and like cron:** use the Rust worker.
- **You want to run it on your own Windows, macOS or Linux PC:** use the desktop app.

## Development

- [Creating a release](development/releasing.md): tags, CI drafts and the manual fallback.
- [Release notes](releases/): one file per version. CI copies `releases/<tag>.md` into the GitHub release.
- Tests: `cargo test --locked --manifest-path worker-server-rust/Cargo.toml`, `go test ./...` in `docker-webui/`, `dotnet test worker-desktop/LingosBot.Tests`.

