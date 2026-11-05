import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import patch
from urllib.error import HTTPError

from deploy import download_release as downloader


ROOT = Path(__file__).resolve().parents[2]


class FakeGitHub(BaseHTTPRequestHandler):
    def do_GET(self):
        self.server.requests.append(self.path)
        self.server.on_request(self.path)
        status, body = self.server.routes.get(self.path, (404, b"Not found"))
        self.send_response(status)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


class DownloadTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), FakeGitHub)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.thread.join()
        cls.server.server_close()

    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.destination = self.root / "release"
        self.data = self.root / "data"
        self.data.mkdir()
        self.saved_data = {"webui.db": b"existing encrypted database",
                           "webui.db-wal": b"existing WAL", "webui.db-shm": b"existing SHM"}
        for name, content in self.saved_data.items():
            (self.data / name).write_bytes(content)
        self.addCleanup(self.assert_data_unchanged)
        self.server.routes = {}
        self.server.requests = []
        self.server.on_request = lambda _: None
        base = f"http://127.0.0.1:{self.server.server_port}"
        for name, value in (("API", base + "/repos/blestafist/LingosBot"),
                            ("DOWNLOADS", base + "/download")):
            patcher = patch.object(downloader, name, value)
            patcher.start()
            self.addCleanup(patcher.stop)
        real_open = downloader.urlopen

        def local_only(request, **kwargs):
            self.assertTrue(request.full_url.startswith(base + "/"), "External network forbidden")
            return real_open(request, **kwargs)

        patcher = patch.object(downloader, "urlopen", local_only)
        patcher.start()
        self.addCleanup(patcher.stop)

    def assert_data_unchanged(self):
        self.assertEqual({p.name: p.read_bytes() for p in self.data.iterdir()}, self.saved_data)

    def json_route(self, path, document):
        self.server.routes["/repos/blestafist/LingosBot" + path] = (200, json.dumps(document).encode())

    def release(self, tag="v1.0.0", *, prerelease=False, draft=False,
                published="2026-11-01T12:00:00Z"):
        assets = []
        for arch in ("x64", "arm64"):
            for component in ("webui", "worker"):
                name = f"lingosbot-{component}-linux-{arch}"
                body = f"{tag}/{name}".encode()
                path = f"/download/{tag}/{name}"
                checksum = f"{hashlib.sha256(body).hexdigest()}  {name}\n".encode()
                for suffix, content in (("", body), (".sha256", checksum)):
                    self.server.routes[path + suffix] = (200, content)
                    assets.append({"name": name + suffix})
        release = {"tag_name": tag, "draft": draft, "prerelease": prerelease,
                   "published_at": published, "assets": assets}
        self.json_route("/releases/tags/" + tag, release)
        return release

    def channel(self, releases):
        for index in range(0, len(releases) + 1, 100):
            self.json_route(f"/releases?per_page=100&page={index // 100 + 1}",
                            releases[index:index + 100])

    def assert_pair(self, tag, arch, destination=None):
        destination = destination or self.destination
        self.assertEqual({p.name for p in destination.iterdir()}, {"webui", "lingosbot", "worker-release.json"})
        manifest = json.loads((destination / "worker-release.json").read_text())
        self.assertEqual(manifest["tag"], tag)
        self.assertEqual(manifest["architecture"], {"x64": "amd64", "arm64": "arm64"}[arch])
        self.assertEqual(manifest["sha256"], hashlib.sha256((destination / "lingosbot").read_bytes()).hexdigest())
        release = json.loads(self.server.routes[f"/repos/blestafist/LingosBot/releases/tags/{tag}"][1])
        self.assertIs(manifest["prerelease"], release["prerelease"])
        for installed, component in (("webui", "webui"), ("lingosbot", "worker")):
            binary = destination / installed
            self.assertEqual(binary.read_bytes(), f"{tag}/lingosbot-{component}-linux-{arch}".encode())
            self.assertEqual(stat.S_IMODE(binary.stat().st_mode), 0o755)

    def assert_not_installed(self):
        self.assertFalse(self.destination.exists())
        self.assertEqual(list(self.root.glob(".lingosbot-*")), [])

    def test_both_architectures_and_pinned_prerelease(self):
        for tag in ("v1.0.0", "v2.0.0-rc.1"):
            self.release(tag, prerelease="rc" in tag)
            for arch, asset_arch in (("amd64", "x64"), ("arm64", "arm64")):
                with self.subTest(tag=tag, arch=arch):
                    destination = self.root / f"{tag}-{arch}"
                    self.assertEqual(downloader.download(tag, arch, destination), tag)
                    self.assert_pair(tag, asset_arch, destination)
        self.assertFalse(any("/releases?" in path for path in self.server.requests))

    def test_channels_use_publication_time_and_exclude_drafts(self):
        releases = [
            self.release("v9.0.0", published="2026-10-01T00:00:00Z"),
            self.release("v1.0.0", published="2026-11-01T00:00:00Z"),
            self.release("v10.0.0-rc.1", prerelease=True, published="2026-11-02T00:00:00Z"),
            self.release("v10.0.0-rc.2", prerelease=True, published="2026-11-03T00:00:00Z"),
            self.release("v99.0.0", draft=True, published="2026-11-04T00:00:00Z"),
            self.release("v99.0.0-rc.1", draft=True, prerelease=True),
            self.release("v100.0.0", published=None),
        ]
        self.channel(releases)
        for channel, tag in (("latest", "v1.0.0"), ("latest-prerelease", "v10.0.0-rc.2")):
            with self.subTest(channel=channel):
                destination = self.root / channel
                self.assertEqual(downloader.download(channel, "amd64", destination), tag)
                self.assert_pair(tag, "x64", destination)

    def test_pagination(self):
        older = self.release("v2.0.0", published="2026-10-01T00:00:00Z")
        newer = self.release("v1.0.0", published="2026-11-01T00:00:00Z")
        self.channel([older] * 100 + [newer])
        self.assertEqual(downloader.download("latest", "arm64", self.destination), "v1.0.0")
        self.assert_pair("v1.0.0", "arm64")
        self.assertIn("/repos/blestafist/LingosBot/releases?per_page=100&page=2", self.server.requests)

    def test_channel_resolves_once_but_next_build_observes_new_release(self):
        first = self.release("v1.0.0")
        second = self.release("v2.0.0", published="2026-11-02T00:00:00Z")
        self.channel([first])

        def publish_during_download(path):
            if path.startswith("/download/"):
                self.channel([second, first])

        self.server.on_request = publish_during_download
        downloader.download("latest", "amd64", self.destination)
        self.assert_pair("v1.0.0", "x64")
        self.assertEqual(sum("/releases?" in path for path in self.server.requests), 1)
        destination = self.root / "next-build"
        downloader.download("latest", "amd64", destination)
        self.assert_pair("v2.0.0", "x64", destination)
        self.assertFalse(any("/latest/" in path for path in self.server.requests))

    def test_no_matching_release(self):
        for channel in ("latest", "latest-prerelease"):
            prerelease = channel == "latest-prerelease"
            for releases in ([], [self.release(prerelease=not prerelease),
                                  self.release("v2.0.0", prerelease=prerelease, draft=True),
                                  self.release("v3.0.0", prerelease=prerelease, published=None)]):
                with self.subTest(channel=channel, releases=len(releases)):
                    self.channel(releases)
                    with self.assertRaisesRegex(ValueError, "No published release"):
                        downloader.download(channel, "amd64", self.destination)
                    self.assert_not_installed()

    def test_latest_does_not_fall_back_to_a_complete_prerelease(self):
        stable = self.release("v1.2.1-Stable")
        stable["assets"] = []
        prerelease = self.release("v2.0.0", prerelease=True, published="2026-11-04T22:31:51Z")
        self.channel([prerelease, stable])
        with self.assertRaisesRegex(ValueError, "Release v1.2.1-Stable.*missing a unique asset"):
            downloader.download("latest", "amd64", self.destination)
        self.assert_not_installed()
        self.assertEqual(downloader.download("latest-prerelease", "amd64", self.destination), "v2.0.0")
        self.assert_pair("v2.0.0", "x64")

    def test_checksum_failure_never_installs_a_partial_pair(self):
        self.release()
        for component in ("webui", "worker"):
            path = f"/download/v1.0.0/lingosbot-{component}-linux-x64"
            original = self.server.routes[path]
            self.server.routes[path] = (200, b"tampered binary")
            with self.subTest(component=component):
                with self.assertRaisesRegex(ValueError, "SHA-256 mismatch"):
                    downloader.download("v1.0.0", "amd64", self.destination)
                self.assert_not_installed()
            self.server.routes[path] = original

    def test_checksum_format_and_filename_are_validated(self):
        self.release()
        name = "lingosbot-webui-linux-x64"
        for checksum in (b"", b"not a checksum", b"a" * 64 + b"  ../../data/webui.db\n",
                         b"a" * 64 + f"  {name}\nextra\n".encode()):
            with self.subTest(checksum=checksum):
                self.server.routes[f"/download/v1.0.0/{name}.sha256"] = (200, checksum)
                with self.assertRaisesRegex(ValueError, "Invalid SHA-256"):
                    downloader.download("v1.0.0", "amd64", self.destination)
                self.assert_not_installed()

    def test_binary_size_limit_preserves_data_and_cleans_partial_pair(self):
        self.release()
        for component in ("webui", "worker"):
            with self.subTest(component=component), patch.object(downloader, "MAX_BINARY_BYTES", 128):
                name = f"lingosbot-{component}-linux-x64"
                path = f"/download/v1.0.0/{name}"
                original = self.server.routes[path]
                original_checksum = self.server.routes[path + ".sha256"]
                body = b"x" * 129
                self.server.routes[path] = (200, body)
                self.server.routes[path + ".sha256"] = (200, f"{hashlib.sha256(body).hexdigest()}  {name}\n".encode())
                with self.assertRaisesRegex(ValueError, "exceeds 128 byte binary size limit"):
                    downloader.download("v1.0.0", "amd64", self.destination)
                self.assert_not_installed()
                self.server.routes[path] = original
                self.server.routes[path + ".sha256"] = original_checksum

    def test_binary_size_limit_accepts_exact_boundary(self):
        self.release()
        maximum = max(len(self.server.routes[f"/download/v1.0.0/lingosbot-{component}-linux-x64"][1])
                      for component in ("webui", "worker"))
        with patch.object(downloader, "MAX_BINARY_BYTES", maximum):
            downloader.download("v1.0.0", "amd64", self.destination)
        self.assert_pair("v1.0.0", "x64")

    def test_missing_or_duplicate_asset_fails_before_download(self):
        for duplicate in (False, True):
            release = self.release()
            if duplicate:
                release["assets"].append(release["assets"][-1])
            else:
                release["assets"].pop()
            self.json_route("/releases/tags/v1.0.0", release)
            with self.subTest(duplicate=duplicate):
                with self.assertRaisesRegex(ValueError, "missing a unique asset"):
                    downloader.download("v1.0.0", "arm64", self.destination)
                self.assert_not_installed()
        self.assertFalse(any(path.startswith("/download/") for path in self.server.requests))

    def test_http_errors_and_unavailable_assets(self):
        for status in (403, 404, 500):
            with self.subTest(status=status):
                self.server.routes["/repos/blestafist/LingosBot/releases/tags/v1.0.0"] = (status, b"Error")
                with self.assertRaises(HTTPError):
                    downloader.download("v1.0.0", "amd64", self.destination)
                self.assert_not_installed()
        self.release()
        del self.server.routes["/download/v1.0.0/lingosbot-worker-linux-x64"]
        with self.assertRaises(HTTPError):
            downloader.download("v1.0.0", "amd64", self.destination)
        self.assert_not_installed()

    def test_invalid_api_responses_and_unpublished_pins(self):
        for document in ({"message": "API failure"}, [None], "invalid"):
            self.json_route("/releases?per_page=100&page=1", document)
            with self.subTest(document=document), self.assertRaises(ValueError):
                downloader.download("latest", "amd64", self.destination)
        for changes in ({"draft": True}, {"published_at": None}, {"tag_name": "v2.0.0"},
                        {"published_at": "bad date"}, {"published_at": 123}, {"assets": None},
                        {"prerelease": None}, {"prerelease": "false"}):
            release = self.release()
            release.update(changes)
            self.json_route("/releases/tags/v1.0.0", release)
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                downloader.download("v1.0.0", "amd64", self.destination)
        self.server.routes["/repos/blestafist/LingosBot/releases/tags/v1.0.0"] = (200, b"{invalid json")
        with self.assertRaises(ValueError):
            downloader.download("v1.0.0", "amd64", self.destination)
        self.assert_not_installed()

    def test_invalid_inputs_and_existing_destination_do_not_touch_data_or_network(self):
        for version, arch, destination in (
            ("../v1.0.0", "amd64", self.destination),
            ("v1.0.0/../../data", "amd64", self.destination),
            ("latest-beta", "amd64", self.destination),
            ("v1.0.0", "armv7", self.destination),
            ("v1.0.0", "amd64", self.data),
        ):
            with self.subTest(version=version, arch=arch), self.assertRaises(ValueError):
                downloader.download(version, arch, destination)
        self.assertEqual(self.server.requests, [])
        self.assert_not_installed()


class DeploymentConfigTests(unittest.TestCase):
    @unittest.skipUnless(shutil.which("docker"), "Docker CLI not installed")
    def test_compose_forces_channel_refresh_and_preserves_runtime_defaults(self):
        for version in (None, "v2.0.1", "latest", "latest-prerelease"):
            # Explicit env file + clean environment: never read the operator's .env/secrets.
            env = {"ADMIN_USER": "test@example.invalid", "ADMIN_PASSWORD": "test-password-only",
                   "ENCRYPTION_KEY": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}
            if version is not None:
                env["RELEASE_VERSION"] = version
            result = subprocess.run(
                [shutil.which("docker"), "compose", "--env-file", "/dev/null", "-f",
                 str(ROOT / "docker-webui/docker-compose.yml"), "config", "--format", "json"],
                env=env, capture_output=True, text=True, check=True,
            )
            config = json.loads(result.stdout)
            service = config["services"]["webui"]
            with self.subTest(version=version):
                self.assertIs(service["build"]["no_cache"], True)
                self.assertEqual(service["build"]["args"]["RELEASE_VERSION"], version or "latest")
                self.assertEqual(service["environment"]["RELEASE_VERSION"], version or "latest")
                self.assertEqual(service["environment"]["ENCRYPTION_KEY"], env["ENCRYPTION_KEY"])
                self.assertEqual(service["environment"]["COOKIE_SECURE"], "1")
                self.assertEqual(service["environment"]["WORKER_BASE_URL"], "https://lingos.pl")
                self.assertEqual(service["environment"]["DB_PATH"], "/data/webui.db")
                self.assertEqual(service["ports"][0]["host_ip"], "127.0.0.1")
                self.assertEqual(service["volumes"][0]["source"], "webui-data")
                self.assertEqual(service["volumes"][0]["target"], "/data")
                self.assertIn("webui-data", config["volumes"])

    def test_image_has_no_compilers_or_runtime_downloader_and_context_is_allowlisted(self):
        dockerfile = (ROOT / "docker-webui/Dockerfile").read_text()
        self.assertNotRegex(dockerfile, r"cargo|golang:|rust:|go build")
        self.assertEqual(dockerfile.count("ARG RELEASE_VERSION=latest"), 2)
        self.assertIn('env("RELEASE_VERSION", "latest")',
                      (ROOT / "docker-webui/internal/webui/worker_release.go").read_text())
        self.assertIn('"$RELEASE_VERSION" "$TARGETARCH" /release', dockerfile)
        final_stage = dockerfile.split("FROM alpine:3.22\n")[1]
        self.assertIn("USER webui", final_stage)
        self.assertIn("chown webui:webui /data", final_stage)
        self.assertIn("VOLUME /data", final_stage)
        self.assertIn("WORKDIR /data", final_stage)
        self.assertIn("COOKIE_SECURE=1", final_stage)
        self.assertNotIn("WORKER_BIN=", final_stage)
        self.assertIn("/usr/local/share/lingosbot/", final_stage)
        self.assertIn('ENTRYPOINT ["/usr/local/bin/webui"]', final_stage)
        self.assertNotIn("python", final_stage)
        patterns = (ROOT / "docker-webui/Dockerfile.dockerignore").read_text().splitlines()
        self.assertEqual([line for line in patterns if line and not line.startswith("#")],
                         ["**", "!deploy/", "!deploy/download_release.py"])
        self.assertIn("/.data/", (ROOT / "docker-webui/.gitignore").read_text().splitlines())


if __name__ == "__main__":
    unittest.main()
