# Creating A Release

Releases are drafted automatically by [`.github/workflows/ci-release.yml`](../.github/workflows/ci-release.yml). One tag `v*` produces one draft containing both the legacy .NET desktop archives and the Rust worker binaries. Commands below use **Bash on Linux**, from the repository root.

## What the workflow does

| Event | Result |
| --- | --- |
| Push to any branch, pull request | Rust (`fmt`, `clippy`, tests) and desktop (`dotnet test`) checks, plus a build of every artifact. **Nothing is published.** |
| Push of a tag `v*`, or a manual run on such a tag | The same checks and builds, then a **draft** release. |

Artifacts of a draft (12 files):

```text
win-x64.zip, linux-x64.zip, macos-x86_64.zip, macos-arm64.zip           (+ .sha256 each)
lingosbot-x86_64-unknown-linux-musl, lingosbot-aarch64-unknown-linux-musl (+ .sha256 each)
```

- Desktop archives are self-contained single-file builds cross-compiled on Linux, each with exactly `LingosBot[.exe]` and `selenium-manager[.exe]` at the root. CI does not run them on Windows or macOS.
- Rust binaries are static musl builds compiled natively on x86_64 and aarch64. Each passes ELF architecture/static-link checks (no `INTERP` or `NEEDED`), `--version`/`--help` smoke checks and `sha256sum -c`. None of these checks need a Lingos account.
- The draft is created only if every test and build succeeded and the tagged commit is contained in `main`.
- Tags containing `-alpha`, `-beta`, `-rc` or `-pre` are created as **pre-releases**.

## Release notes come from the tagged commit

The title is the tag name. The description is the full message of the commit the tag points to, so write the notes in that commit message:

```bash
git switch main && git pull --ff-only
git status --short
git commit --allow-empty -F - <<'EOF_NOTES'
Release v1.6.0

## Changes
- What changed for users.

## Compatibility
- Anything users must know.
EOF_NOTES
git tag v1.6.0
git push origin main v1.6.0
```

You can also tag an ordinary last commit; its message then becomes the notes. Edited notes are never overwritten: rerunning the workflow for a tag whose draft exists only refreshes the files (`--clobber`). If a release for the tag is already published, the workflow fails without changing it.

## Review and publish

Open the draft in GitHub Releases, check the notes and all 12 assets, then publish (for a stable release mark it as latest). The workflow never publishes by itself. Installation of the worker binaries is documented in the [Rust worker README](../worker-server-rust/README.md).

## Manual fallback

The remaining sections describe the manual process used before the workflow existed. Use them only if the workflow is unavailable.

## Legacy Desktop Prerequisites

The remaining checklist builds and publishes the four archives for the existing .NET application in `worker-desktop/`.

- .NET **10 SDK** (`dotnet --version`).
- Git, GitHub CLI (`gh`), and **7-Zip** with the `7z` command available.
- GitHub authentication: `gh auth login` (with permission to create releases in `blestafist/LingosBot`).
- Internet access to restore .NET runtime packs and NuGet packages.

| Platform | .NET runtime identifier | Release asset |
| --- | --- | --- |
| Windows x64 | `win-x64` | `win-x64.zip` |
| Linux x64 | `linux-x64` | `linux-x64.zip` |
| macOS Intel | `osx-x64` | `macos-x86_64.zip` |
| macOS Apple Silicon | `osx-arm64` | `macos-arm64.zip` |

These are self-contained, single-file application builds: users do not need .NET installed. Selenium Manager remains a **separate binary** and must also be included for optional browser mode. The default API mode requires no browser or driver.

## 1. Select And Test The Release Commit

Merge the intended changes into `main` first. Start with a clean working tree; do not switch branches over unfinished work.

```bash
git switch main
git pull --ff-only https://github.com/blestafist/LingosBot.git main
git status --short
dotnet test worker-desktop/LingosBot.Tests/LingosBot.Tests.csproj -c Release
```

Stop if there are unexpected local changes or failing tests. Choose a **new, unused** tag and record the exact commit. Keep using the same Bash session for the following commands.

```bash
TAG=v1.2.1-Stable  # Example only: choose the actual release version.
RELEASE_COMMIT=$(git rev-parse HEAD)
```

## 2. Build And ZIP All Four Platforms

Paste this entire block into Bash. It stops on errors and uses a fresh directory under ignored `bin/` for every attempt, so old binaries or ZIP entries cannot leak into a new release.

```bash
set -euo pipefail

mkdir -p bin/releases
RELEASE_DIR=$(mktemp -d "$PWD/bin/releases/release.XXXXXXXX")
mkdir -p "$RELEASE_DIR/assets"

for rid in win-x64 linux-x64 osx-x64 osx-arm64; do
    case "$rid" in
        win-x64)   asset=win-x64;      app=LingosBot.exe; manager=selenium-manager.exe ;;
        linux-x64) asset=linux-x64;    app=LingosBot;     manager=selenium-manager ;;
        osx-x64)   asset=macos-x86_64; app=LingosBot;     manager=selenium-manager ;;
        osx-arm64) asset=macos-arm64;  app=LingosBot;     manager=selenium-manager ;;
    esac

    output="$RELEASE_DIR/$rid"
    dotnet publish worker-desktop/LingosBot.csproj \
        -c Release -r "$rid" --self-contained true \
        -p:PublishSingleFile=true \
        -p:DebugType=None -p:DebugSymbols=false \
        -o "$output"

    test -s "$output/$app"
    test -s "$output/$manager"
    if [[ "$rid" != win-x64 ]]; then
        chmod +x "$output/$app" "$output/$manager"
    fi

    (
        cd "$output"
        7z a -tzip -mx=9 "$RELEASE_DIR/assets/$asset.zip" "$app" "$manager"
    )
    7z t "$RELEASE_DIR/assets/$asset.zip"
    7z l "$RELEASE_DIR/assets/$asset.zip"
done
```

`-tzip` explicitly selects ZIP; **`-mx=9` selects maximum compression**. Only the two named binaries are archived, not the entire publish directory.

With the current Selenium.WebDriver package, `dotnet publish` puts the platform-specific Selenium Manager beside the application automatically. Do not embed it using `IncludeNativeLibrariesForSelfExtract`, substitute another platform's binary, or move it into an extra directory. If either `test -s` check fails after a dependency update, inspect the publish output before continuing.

Each ZIP must contain exactly these files **at its root**, without a wrapping folder:

```text
win-x64.zip
├── LingosBot.exe
└── selenium-manager.exe

linux-x64.zip / macos-x86_64.zip / macos-arm64.zip
├── LingosBot
└── selenium-manager
```

Never include personal `config.json`, credentials, cookies, diagnostics, browser profiles, or saved pages.

## 3. Check The Artifacts

- Confirm all four archives passed `7z t` and their listings contain only the expected two files.
- Extract each archive on its target OS and check that both binaries run. Cross-compiling on Linux does **not** verify execution on Windows or either Mac architecture.
- On Linux/macOS, restore executable permissions if the extraction tool did not preserve them: `chmod +x LingosBot selenium-manager`.
- Check Selenium Manager with `./selenium-manager --version` (Windows: `.\selenium-manager.exe --version`).
- Before publishing, verify the application using a test account: `./LingosBot` (Windows: `.\LingosBot.exe`). By default this tests browser-free API mode. Test optional browser startup separately with `--mode browser --headless`. These are real runs and can complete lessons; do not use an account whose lesson progress must remain unchanged. There is no `--help` or dry-run option.

## 4. Upload A Draft To GitHub

Use the same Bash session (`TAG`, `RELEASE_COMMIT`, and `RELEASE_DIR` must still be set). Write release notes describing the actual changes and any compatibility warnings:

```bash
NOTES="$RELEASE_DIR/release-notes.md"
cat > "$NOTES" <<'EOF'
## Changes
- Replace this line with the changes included in this release.

## Installation
Download the ZIP for your platform and extract both files into the same directory.
No browser is required for the default API mode. A supported browser is required only for `mode: "browser"`; installing .NET is not necessary.
EOF

# Edit the notes before continuing.
${EDITOR:-nano} "$NOTES"

gh release create "$TAG" \
    "$RELEASE_DIR/assets/win-x64.zip" \
    "$RELEASE_DIR/assets/linux-x64.zip" \
    "$RELEASE_DIR/assets/macos-x86_64.zip" \
    "$RELEASE_DIR/assets/macos-arm64.zip" \
    --repo blestafist/LingosBot \
    --target "$RELEASE_COMMIT" \
    --title "${TAG/-/ }" \
    --notes-file "$NOTES" \
    --draft
```

This creates a **draft**, not a public release. GitHub creates the new tag at the specified commit as part of the release workflow; no separate `git push --tags` is needed. The explicit repository avoids accidentally publishing to a different remote.

Alternatively, open [New release](https://github.com/blestafist/LingosBot/releases/new), create the new tag targeting the recorded commit, enter the title and notes, attach the four ZIPs from `$RELEASE_DIR/assets`, and save a draft.

## 5. Review And Publish

```bash
gh release view "$TAG" --repo blestafist/LingosBot --web
```

Check the tag/commit, notes, and all four downloadable ZIP assets. Only after approval, publish the stable release and mark it as latest:

```bash
gh release edit "$TAG" --repo blestafist/LingosBot --draft=false --latest
gh release view "$TAG" --repo blestafist/LingosBot
```

For a prerelease, mark it as a prerelease instead of using `--latest`. GitHub's automatically generated source-code archives are not substitutes for the four binary ZIPs.
