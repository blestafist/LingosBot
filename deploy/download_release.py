"""Install matching, checksum-verified release binaries; no compilers required."""

import argparse
from datetime import datetime
import hashlib
import json
from pathlib import Path
import re
import sys
import tempfile
from urllib.error import HTTPError
from urllib.parse import quote
from urllib.request import Request, urlopen


API = "https://api.github.com/repos/blestafist/LingosBot"
DOWNLOADS = "https://github.com/blestafist/LingosBot/releases/download"
TAG = re.compile(r"v[0-9][0-9A-Za-z.+-]*")
# shortcut: cap each binary at 64 MiB; raise if future release binaries outgrow it.
MAX_BINARY_BYTES = 64 * 1024 * 1024


def open_url(url):
    try:
        return urlopen(Request(url, headers={"User-Agent": "LingosBot-deploy"}), timeout=60)
    except HTTPError as error:
        error.close()
        raise


def api(path):
    with open_url(API + path) as response:
        return json.load(response)


def resolve_release(version):
    if version in ("latest", "latest-prerelease"):
        prerelease = version == "latest-prerelease"
        candidates = []
        page = 1
        while True:
            batch = api(f"/releases?per_page=100&page={page}")
            if not isinstance(batch, list) or any(not isinstance(r, dict) for r in batch):
                raise ValueError("Invalid GitHub releases response")
            candidates.extend(
                r for r in batch
                if r.get("draft") is False and r.get("prerelease") is prerelease
                and r.get("published_at")
            )
            if len(batch) < 100:
                break
            page += 1
        if not candidates:
            raise ValueError(f"No published release matches {version}")
        # GitHub lists by creation, not publication; channels follow publication time.
        release = max(candidates, key=publication_time)
    elif TAG.fullmatch(version):
        release = api("/releases/tags/" + quote(version, safe=""))
        if not isinstance(release, dict) or release.get("tag_name") != version:
            raise ValueError("GitHub returned a different release tag")
    else:
        raise ValueError("Version must be v... (a release tag), latest or latest-prerelease")

    if release.get("draft") is not False or not release.get("published_at"):
        raise ValueError("The release is not published (drafts are excluded)")
    tag = release.get("tag_name")
    if not isinstance(tag, str) or not TAG.fullmatch(tag):
        raise ValueError("Invalid release tag")
    if not isinstance(release.get("prerelease"), bool):
        raise ValueError("Invalid release prerelease flag")
    publication_time(release)
    return release


def publication_time(release):
    published = release.get("published_at")
    if not isinstance(published, str):
        raise ValueError("Invalid release publication time")
    return datetime.strptime(published, "%Y-%m-%dT%H:%M:%SZ")


def download(version, architecture, destination):
    arch = {"amd64": "x64", "arm64": "arm64"}.get(architecture)
    if arch is None:
        raise ValueError("Only linux/amd64 and linux/arm64 are supported")
    destination = Path(destination)
    if destination.exists() or destination.is_symlink():
        raise ValueError("Destination already exists; refusing to overwrite it")
    release = resolve_release(version)
    tag = release["tag_name"]
    assets = release.get("assets")
    if not isinstance(assets, list):
        raise ValueError(f"Release {tag} has no assets")
    binaries = {f"lingosbot-webui-linux-{arch}": "webui",
                f"lingosbot-worker-linux-{arch}": "lingosbot"}
    for name in binaries:
        for required in (name, name + ".sha256"):
            if sum(isinstance(a, dict) and a.get("name") == required for a in assets) != 1:
                raise ValueError(f"Release {tag} is missing a unique asset: {required}")

    print(f"Installing LingosBot {tag} (linux/{architecture})", flush=True)
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".lingosbot-", dir=destination.parent) as temp:
        stage = Path(temp) / "release"
        stage.mkdir()
        for name, installed in binaries.items():
            # Resolve the channel once; never use moving /latest/download URLs.
            url = f"{DOWNLOADS}/{quote(tag, safe='')}/{name}"
            with open_url(url + ".sha256") as response:
                checksum = response.read(1024).decode("ascii")
            match = re.fullmatch(r"([0-9a-fA-F]{64}) [ *]" + re.escape(name) + r"\n?", checksum)
            if match is None:
                raise ValueError(f"Invalid SHA-256 file for {name}")
            digest = hashlib.sha256()
            binary = stage / installed
            written = 0
            with open_url(url) as response, binary.open("wb") as output:
                while chunk := response.read(min(1024 * 1024, MAX_BINARY_BYTES - written + 1)):
                    written += len(chunk)
                    if written > MAX_BINARY_BYTES:
                        raise ValueError(f"{name} exceeds {MAX_BINARY_BYTES} byte binary size limit")
                    digest.update(chunk)
                    output.write(chunk)
            if digest.hexdigest() != match[1].lower():
                raise ValueError(f"SHA-256 mismatch for {name}")
            binary.chmod(0o755)
            if installed == "lingosbot":
                (stage / "worker-release.json").write_text(
                    json.dumps({"tag": tag, "sha256": digest.hexdigest(), "architecture": architecture,
                                "prerelease": release["prerelease"]}) + "\n"
                )
        # Publish the pair only after both checks pass; existing data is never touched.
        stage.rename(destination)
    return tag


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version")
    parser.add_argument("architecture", help="amd64 or arm64 (Docker TARGETARCH)")
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    try:
        download(args.version, args.architecture, args.destination)
    except (OSError, ValueError) as error:
        print(f"Release installation failed: {error}", file=sys.stderr)
        sys.exit(1)
