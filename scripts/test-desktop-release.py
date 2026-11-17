#!/usr/bin/env python3
import importlib.util
import hashlib
import json
import subprocess
import sys
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("desktop_release", Path(__file__).with_name("desktop-release.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)
spec = importlib.util.spec_from_file_location("apple_timestamp", Path(__file__).with_name("package-with-apple-timestamp.py"))
timestamp = importlib.util.module_from_spec(spec)
spec.loader.exec_module(timestamp)


class AppleTimestampTest(unittest.TestCase):
    command = ["npm", "run", "package", "--", "--mac", "--arm64", "--dir",
               "-c.mac.identity=(WV3J3848JR)", "-c.mac.notarize=true"]

    def runner(self, results):
        calls = []
        def run(command):
            calls.append(command)
            code, output = results[len(calls) - 1]
            return subprocess.CompletedProcess(command, code, output)
        return calls, run

    def test_normal_success_does_not_resolve_or_retry(self):
        calls, run = self.runner([(0, "Notarized")])
        self.assertEqual(timestamp.package(self.command, run, lambda: self.fail("unexpected DNS")), 0)
        self.assertEqual(calls, [self.command])

    def test_only_missing_timestamps_trigger_fallback(self):
        calls, run = self.runner([(7, "Signing identity missing")])
        self.assertEqual(timestamp.package(self.command, run, lambda: self.fail("unexpected DNS")), 7)
        self.assertEqual(len(calls), 1)

    def test_cli_executes_the_command_and_preserves_failure(self):
        result = subprocess.run([sys.executable, str(Path(__file__).with_name("package-with-apple-timestamp.py")),
                                 "--", sys.executable, "-c", "print('signing fixture failed'); raise SystemExit(7)"],
                                text=True, capture_output=True)
        self.assertEqual(result.returncode, 7)
        self.assertIn("signing fixture failed", result.stdout)

    def test_ipv4_fallback_preserves_signing_notarization_and_has_bounded_attempts(self):
        calls, run = self.runner([(1, timestamp.TIMESTAMP_ERROR), (1, timestamp.TIMESTAMP_ERROR), (0, "Notarized")])
        self.assertEqual(timestamp.package(self.command, run, lambda: ["17.32.213.1", "17.32.213.1", "127.0.0.1", "17.179.249.1"]), 0)
        self.assertEqual(calls[1], [*self.command, "-c.mac.timestamp=http://17.32.213.1/ts01"])
        self.assertEqual(calls[2], [*self.command, "-c.mac.timestamp=http://17.179.249.1/ts01"])

    def test_notarization_failure_after_retry_is_not_retried_or_accepted(self):
        calls, run = self.runner([(1, timestamp.TIMESTAMP_ERROR), (9, "Notarization rejected")])
        self.assertEqual(timestamp.package(self.command, run, lambda: ["17.32.213.1", "17.32.213.2"]), 9)
        self.assertEqual(len(calls), 2)

    def test_unavailable_or_untrusted_addresses_keep_signing_failed(self):
        for addresses in [[], ["127.0.0.1"], ["invalid"]]:
            calls, run = self.runner([(3, timestamp.TIMESTAMP_ERROR)])
            self.assertEqual(timestamp.package(self.command, run, lambda: addresses), 3)
            self.assertEqual(len(calls), 1)
        calls, run = self.runner([(3, timestamp.TIMESTAMP_ERROR)])
        with patch.object(timestamp.socket, "getaddrinfo", side_effect=OSError("DNS unavailable")):
            self.assertEqual(timestamp.package(self.command, run), 3)

    def test_no_more_than_three_ipv4_retries(self):
        calls, run = self.runner([(4, timestamp.TIMESTAMP_ERROR)] * 4)
        self.assertEqual(timestamp.package(self.command, run, lambda: [f"17.32.213.{i}" for i in range(1, 10)]), 4)
        self.assertEqual(len(calls), 4)


class DesktopReleaseTest(unittest.TestCase):
    def test_source_version_and_changelog_match(self):
        package, changes = release.validate()
        self.assertEqual(package["version"], changes["version"])

    def test_publication_requires_every_target_and_records_integrity(self):
        package, _ = release.validate()
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            with self.assertRaises(ValueError):
                release.stage(directory)
            for platform in ("mac", "win", "linux"):
                for arch in ("x64", "arm64"):
                    extension = "AppImage" if platform == "linux" else "zip"
                    (directory / f"PrivateStore-{package['version']}-{platform}-{arch}.{extension}").write_bytes(b"verified package")
            _, manifest = release.stage(directory)
            self.assertEqual(6, len(manifest["artifacts"]))
            self.assertTrue(all(len(a["sha256"]) == 64 and a["size"] == 16 for a in manifest["artifacts"]))
            self.assertIn("desktop-release.json", (directory / "SHA256SUMS").read_text())


class ThornwakeReleaseTest(unittest.TestCase):
    def build_manifest(self, changelog, version="1.2.0", game="thornwake"):
        script = Path(__file__).with_name("package-thornwake-release.sh").read_text()
        # Run the publisher's actual manifest step without requiring signing credentials.
        generator = script.split("<<'PY'\n")[-1].rsplit("\nPY", 1)[0]
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            name = "Thornwake" if game == "thornwake" else "Ember Rest"
            filename = ("Thornwake" if game == "thornwake" else "EmberRest") + "-" + version + "-mac-universal.zip"
            (directory / filename).write_bytes(b"signed and stapled package")
            notes = directory / "changelog.json"
            notes.write_text(json.dumps(changelog))
            result = subprocess.run([sys.executable, "-", str(directory), filename, version, name, game, str(release.ROOT / "scripts/native-game.cjs"), str(notes)],
                input=generator, text=True, capture_output=True)
            if result.returncode:
                return result, None
            return result, json.loads((directory / "desktop-release.json").read_text())

    def test_thornwake_release_records_current_notes_and_both_architectures(self):
        changelog = json.loads((release.ROOT / "desktop/thornwake-changelog.json").read_text())
        result, manifest = self.build_manifest(changelog, changelog["version"])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(manifest["changes"], changelog["changes"])
        self.assertEqual(manifest["versionCode"], changelog["versionCode"])
        self.assertEqual({a["arch"] for a in manifest["artifacts"]}, {"arm64", "x64"})
        for artifact in manifest["artifacts"]:
            self.assertEqual(artifact["sha256"], hashlib.sha256(b"signed and stapled package").hexdigest())
            self.assertEqual(artifact["size"], len(b"signed and stapled package"))

    def test_shared_publisher_preserves_ember_rest_metadata(self):
        result, manifest = self.build_manifest({}, "1.0.1", "ember-rest")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(manifest["version"], "1.0.1")
        self.assertEqual(manifest["versionCode"], 1000001)
        self.assertEqual(manifest["artifacts"][0]["entry"], "Ember Rest.app")
        self.assertIn("continuous actor interpolation", manifest["changes"][0])
        self.assertTrue(any("dead zone" in change for change in manifest["changes"]))
        self.assertEqual({a["arch"] for a in manifest["artifacts"]}, {"arm64", "x64"})

    def test_thornwake_rejects_mismatched_or_invalid_notes(self):
        base = {"schemaVersion": 1, "version": "1.2.0", "versionCode": 1002000, "changes": ["Updated units."]}
        for change in [{"version": "1.1.0"}, {"versionCode": 1}, {"schemaVersion": 2},
                       {"changes": []}, {"changes": [" "]}, {"changes": ["Line\nbreak"]},
                       {"changes": ["x" * 2001]}, {"changes": ["Updated."] * 51}]:
            with self.subTest(change=change):
                result, manifest = self.build_manifest({**base, **change})
                self.assertNotEqual(result.returncode, 0)
                self.assertIsNone(manifest)


if __name__ == "__main__":
    unittest.main()
