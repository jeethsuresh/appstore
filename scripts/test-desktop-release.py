#!/usr/bin/env python3
import importlib.util
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


if __name__ == "__main__":
    unittest.main()
