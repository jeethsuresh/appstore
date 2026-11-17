#!/usr/bin/env python3
import importlib.util
import hashlib
import json
import subprocess
import sys
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("desktop_release", Path(__file__).with_name("desktop-release.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


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
        result, manifest = self.build_manifest({}, "1.0.0", "ember-rest")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(manifest["version"], "1.0.0")
        self.assertEqual(manifest["artifacts"][0]["entry"], "Ember Rest.app")
        self.assertIn("144-room", manifest["changes"][0])

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
