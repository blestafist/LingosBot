# Creating A Release

Maintainer checklist for building and publishing the four GitHub release archives. Commands below use **Bash on Linux**, from the repository root. They replace the old build notes in `requirements.txt`.

## Prerequisites

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

These are self-contained, single-file application builds: users do not need .NET installed. Selenium Manager remains a **separate binary** and must also be included. A supported browser is still required.

## 1. Select And Test The Release Commit

Merge the intended changes into `main` first. Start with a clean working tree; do not switch branches over unfinished work.

```bash
git switch main
git pull --ff-only https://github.com/blestafist/LingosBot.git main
git status --short
dotnet test LingosBot.Tests/LingosBot.Tests.csproj -c Release
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
    dotnet publish LingosBot.csproj \
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
- Before publishing, verify the application and browser startup using a test account: `./LingosBot` (Windows: `.\LingosBot.exe`). This is a real run and can complete lessons; do not use an account whose lesson progress must remain unchanged. There is no `--help` or dry-run option.

## 4. Upload A Draft To GitHub

Use the same Bash session (`TAG`, `RELEASE_COMMIT`, and `RELEASE_DIR` must still be set). Write release notes describing the actual changes and any compatibility warnings:

```bash
NOTES="$RELEASE_DIR/release-notes.md"
cat > "$NOTES" <<'EOF'
## Changes
- Replace this line with the changes included in this release.

## Installation
Download the ZIP for your platform and extract both files into the same directory.
A supported browser is required; installing .NET is not necessary.
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
