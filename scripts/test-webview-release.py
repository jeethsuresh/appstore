import base64
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("webview_release", Path(__file__).with_name("webview-release.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class WebViewReleaseChecks(unittest.TestCase):
    def setUp(self):
        self.config = {"version": "0.1.0", "versionCode": 1}

    def test_production_catalog_versions_and_pins_match(self):
        config = release.configuration()
        self.assertEqual([app["id"] for app in config["apps"]], ["work", "news"])

    def test_first_release_does_not_confuse_store_or_desktop_versions(self):
        def request(path, missing=False):
            if missing: return None
            return [{"tag_name": "v0.6.10", "draft": False, "prerelease": False},
                    {"tag_name": "desktop-v5.0.0", "draft": False, "prerelease": False}]
        self.assertTrue(release.plan(self.config, request))

    def test_published_versions_are_skipped_and_drafts_blocked(self):
        self.assertFalse(release.plan(self.config, lambda *args, **kwargs: {"draft": False}))
        with self.assertRaises(ValueError):
            release.plan(self.config, lambda *args, **kwargs: {"draft": True})

    def test_existing_tags_are_never_overwritten(self):
        def request(path, missing=False):
            return {} if path.startswith("releases/") else {"ref": "already exists"}
        with self.assertRaises(ValueError): release.plan(self.config, request)

    def test_upgrade_requires_increasing_version_and_code(self):
        def request(path, missing=False):
            if missing: return None
            if path.startswith("contents/"):
                return {"content": base64.b64encode(json.dumps({"versionCode": 1}).encode()).decode()}
            return [{"tag_name": "webview-v0.1.0", "draft": False, "prerelease": False}]
        for config in ({"version": "0.1.0", "versionCode": 2}, {"version": "0.2.0", "versionCode": 1}):
            with self.assertRaises(ValueError): release.plan(config, request)
        self.assertTrue(release.plan({"version": "0.2.0", "versionCode": 2}, request))

    def test_wrong_apk_signature_or_package_cannot_be_staged(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            config = {**self.config, "certificateSha256": "a" * 64, "apps": [{"id": "work", "package": "cool.gravitas.work", "asset": "work-android.apk"}]}
            for cert, package in (("b" * 64, "cool.gravitas.work"), ("a" * 64, "evil.package")):
                signature = "Signer #1 certificate SHA-256 digest: " + cert + "\nVerified using v2 scheme (APK Signature Scheme v2): true"
                metadata = f"package: name='{package}' versionCode='1' versionName='0.1.0'\nminSdkVersion:'26'"
                with patch.object(release, "ROOT", root), patch.dict(os.environ, {"ANDROID_HOME": directory}), \
                     patch.object(release.subprocess, "check_output", side_effect=[signature, metadata]):
                    with self.assertRaises(ValueError): release.stage(config)

    def test_missing_staged_assets_prevent_upload(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "webview-dist").mkdir()
            with patch.object(release, "ROOT", root), patch.object(release, "plan", return_value=True), \
                 patch.object(release.subprocess, "run") as run:
                with self.assertRaises(ValueError): release.publish({**self.config, "apps": []})
                run.assert_not_called()


if __name__ == "__main__": unittest.main()
