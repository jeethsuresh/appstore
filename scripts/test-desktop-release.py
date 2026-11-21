#!/usr/bin/env python3
import importlib.util
import hashlib
import json
import subprocess
import sys
from pathlib import Path
import tempfile
import unittest
from zipfile import ZipFile

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
            with self.assertRaisesRegex(ValueError, "seven"):
                release.stage(directory)
            (directory / f"PrivateStore-{package['version']}-linux-x64.flatpak").write_bytes(b"verified package")
            _, manifest = release.stage(directory)
            self.assertEqual(7, len(manifest["artifacts"]))
            flatpak = next(a for a in manifest["artifacts"] if a.get("format") == "flatpak")
            self.assertEqual(flatpak["entry"], package["build"]["appId"])
            self.assertEqual(flatpak["arch"], "x64")
            self.assertTrue(all(len(a["sha256"]) == 64 and a["size"] == 16 for a in manifest["artifacts"]))
            self.assertIn("desktop-release.json", (directory / "SHA256SUMS").read_text())
            (directory / f"PrivateStore-{package['version']}-linux-arm64.flatpak").write_bytes(b"unsupported target")
            with self.assertRaisesRegex(ValueError, "incompatible"):
                release.stage(directory)


class ThornwakeReleaseTest(unittest.TestCase):
    def test_native_signer_rejects_invalid_version_before_loading_credentials(self):
        signer = Path(__file__).with_name("package-thornwake-release.sh")
        result = subprocess.run(["/bin/bash", str(signer), "01.1.0", "input.zip", "out"],
                                capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Invalid native game version", result.stderr)

    def build_manifest(self, changelog, version="1.2.0", game="thornwake"):
        script = Path(__file__).with_name("package-thornwake-release.sh").read_text()
        # Run the publisher's actual manifest step without requiring signing credentials.
        generator = script.split("<<'PY'\n")[-1].rsplit("\nPY", 1)[0]
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            name = {"thornwake":"Thornwake", "ember-rest":"Ember Rest", "railyard-fps":"Railyard FPS"}[game]
            filename = name.replace(" ", "") + "-" + version + "-mac-universal.zip"
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

    def test_pure_godot_archive_and_manifest(self):
        result, manifest = self.build_manifest({}, "0.4.0", "railyard-fps")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(manifest["versionCode"], 4000)
        self.assertEqual({a["arch"] for a in manifest["artifacts"]}, {"arm64", "x64"})
        self.assertTrue(all(a["file"] == "RailyardFPS-0.4.0-mac-universal.zip" and
                            a["entry"] == "Railyard FPS.app" for a in manifest["artifacts"]))

        prefix = "Railyard FPS.app/Contents/"
        binary = prefix + "MacOS/Railyard FPS"
        pck = prefix + "Resources/Railyard FPS.pck"
        base = {binary:b"executable fixture", pck:b"GDPCpack fixture"}
        cases = [(base, True), ({binary: b"executable fixture"}, False), ({**base, pck: b"not a pack"}, False),
                 ({**base, prefix + "Frameworks/unlisted.dylib": b"unexpected"}, False)]
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / "input.zip"
            for files, valid in cases:
                with self.subTest(files=list(files), valid=valid):
                    with ZipFile(archive, "w") as z:
                        for path, data in files.items():
                            z.writestr(path, data)
                    check = subprocess.run([sys.executable, str(Path(__file__).with_name("validate-native-archive.py")),
                                            str(archive), "Railyard FPS", "Railyard FPS.pck"], capture_output=True)
                    self.assertEqual(check.returncode == 0, valid, check.stderr)

    def test_shared_publisher_preserves_ember_rest_metadata(self):
        result, manifest = self.build_manifest({}, "1.0.2", "ember-rest")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(manifest["version"], "1.0.2")
        self.assertEqual(manifest["versionCode"], 1000002)
        self.assertEqual(manifest["artifacts"][0]["entry"], "Ember Rest.app")
        self.assertIn("navigation stalls", manifest["changes"][0])
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
