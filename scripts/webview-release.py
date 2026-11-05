#!/usr/bin/env python3
"""Verify and publish Work and News as an immutable, separately versioned APK pair."""
import argparse
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("android_release", ROOT / "scripts/android-release.py")
android = importlib.util.module_from_spec(spec)
spec.loader.exec_module(android)


def configuration():
    config = json.loads((ROOT / "webview-release.json").read_text())
    source = (ROOT / "webview-android/app/build.gradle.kts").read_text()
    name, code = android.version(source, "gradle")
    if (name, code) != (config["version"], config["versionCode"]):
        raise ValueError("WebView source and release versions must match")
    android.changelog((ROOT / "webview-android/changelog.json").read_bytes(), name, code)
    catalog = json.loads((ROOT / "config/apps.json").read_text())
    for app in config["apps"]:
        entry = next(item for item in catalog if item["id"] == app["id"])
        if (entry["packageName"], entry["assetName"], entry["certificateSha256"], entry["repository"], entry.get("releaseTagPrefix")) != (
                app["package"], app["asset"], config["certificateSha256"], "jeethsuresh/appstore", "webview-v"):
            raise ValueError("Store catalog differs from the WebView release identity")
        for required in ('applicationId = "' + app["package"] + '"', app["origin"]):
            if required not in source:
                raise ValueError("WebView source differs from the release identity")
    return config


def plan(config, request=android.api):
    name, code = config["version"], config["versionCode"]
    tag = "webview-v" + name
    existing = request("releases/tags/" + tag, missing=True)
    if existing:
        if existing["draft"]: raise ValueError("An existing draft requires review")
        return False
    if request("git/ref/tags/" + tag, missing=True):
        raise ValueError("Tag already exists; increase the WebView version")
    for page in range(1, 11):
        releases = request(f"releases?per_page=100&page={page}")
        for release in releases:
            old_tag = release["tag_name"]
            if release["draft"] or release["prerelease"] or not old_tag.startswith("webview-v"): continue
            old_name = old_tag.removeprefix("webview-v")
            if android.semver(name) <= android.semver(old_name):
                raise ValueError("WebView version must increase")
            old = request("contents/webview-release.json?ref=" + old_tag)
            old_config = json.loads(base64.b64decode(old["content"]))
            if code <= old_config["versionCode"]:
                raise ValueError("WebView version code must increase")
        if len(releases) < 100: return True
    raise ValueError("Release history exceeds the validation limit")


def stage(config):
    dest = ROOT / "webview-dist"
    if dest.exists(): shutil.rmtree(dest)
    dest.mkdir()
    tools = Path(os.environ["ANDROID_HOME"]) / "build-tools/35.0.0"
    manifests = []
    for app in config["apps"]:
        apk = ROOT / f"webview-android/app/build/outputs/apk/{app['id']}/release/app-{app['id']}-release.apk"
        signature = subprocess.check_output([str(tools / "apksigner"), "verify", "--verbose", "--print-certs", str(apk)], text=True)
        metadata = subprocess.check_output([str(tools / "aapt2"), "dump", "badging", str(apk)], text=True)
        android.apk_identity({"package": app["package"], "certificate_sha256": config["certificateSha256"], "min_sdk": 26},
                             config["version"], config["versionCode"], signature, metadata)
        asset = dest / app["asset"]
        shutil.copyfile(apk, asset)
        manifests.append({**app, "version": config["version"], "versionCode": config["versionCode"],
                          "certificateSha256": config["certificateSha256"], "sha256": hashlib.sha256(asset.read_bytes()).hexdigest(), "size": asset.stat().st_size})
    shutil.copyfile(ROOT / "webview-android/changelog.json", dest / "changelog.json")
    (dest / "webview-release.json").write_text(json.dumps({"schemaVersion": 1, "commit": os.environ["GITHUB_SHA"], "apps": manifests}, indent=2) + "\n")
    (dest / "SHA256SUMS").write_text("".join(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n" for path in sorted(dest.iterdir())))


def publish(config):
    tag = "webview-v" + config["version"]
    if not plan(config): return
    dest = ROOT / "webview-dist"
    expected = {app["asset"] for app in config["apps"]} | {"changelog.json", "webview-release.json", "SHA256SUMS"}
    if {p.name for p in dest.iterdir()} != expected: raise ValueError("Incomplete staged release")
    for line in (dest / "SHA256SUMS").read_text().splitlines():
        digest, filename = line.split("  ")
        if filename not in expected or hashlib.sha256((dest / filename).read_bytes()).hexdigest() != digest:
            raise ValueError("Staged asset changed")
    manifest = json.loads((dest / "webview-release.json").read_text())
    if manifest["commit"] != os.environ["GITHUB_SHA"]: raise ValueError("Staged source commit changed")
    changes = android.changelog((dest / "changelog.json").read_bytes(), config["version"], config["versionCode"])
    if len(manifest["apps"]) != len(config["apps"]): raise ValueError("Missing APK metadata")
    for app in config["apps"]:
        staged = next(item for item in manifest["apps"] if item["id"] == app["id"])
        if any(staged[key] != value for key, value in app.items()) or (
                staged["version"], staged["versionCode"], staged["certificateSha256"]) != (
                config["version"], config["versionCode"], config["certificateSha256"]):
            raise ValueError("Staged APK identity changed")
        asset = dest / app["asset"]
        if staged["size"] != asset.stat().st_size or staged["sha256"] != hashlib.sha256(asset.read_bytes()).hexdigest():
            raise ValueError("Staged APK changed")
    files = sorted(dest.iterdir())
    repo = os.environ["GITHUB_REPOSITORY"]
    subprocess.run(["gh", "release", "create", tag, "--repo", repo, "--target", os.environ["GITHUB_SHA"], "--draft",
                    "--title", "Work and News " + config["version"], "--notes", "\n".join("- " + text for text in changes["changes"]), *map(str, files)], check=True)
    uploaded = android.uploaded_draft(tag)
    assets = {item["name"]: item for item in uploaded["assets"]}
    if set(assets) != expected: raise ValueError("Incomplete upload; draft remains unpublished")
    for path in files:
        item = assets[path.name]
        if item["state"] != "uploaded" or item["size"] != path.stat().st_size or item.get("digest") != "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest():
            raise ValueError("Upload verification failed; draft remains unpublished")
    subprocess.run(["gh", "release", "edit", tag, "--repo", repo, "--draft=false", "--latest=false"], check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["validate", "plan", "build", "stage", "publish"])
    action = parser.parse_args().action
    config = configuration()
    if action == "plan":
        run = plan(config)
        with open(os.environ["GITHUB_OUTPUT"], "a") as output: output.write(f"build={str(run).lower()}\n")
    elif action == "build":
        android.build({"key_alias": "private-store-release", "certificate_sha256": config["certificateSha256"],
                       "project_dir": "webview-android", "build": ["./gradlew", "--no-daemon", "assembleRelease", "lintWorkRelease", "lintNewsRelease"]})
    elif action == "stage": stage(config)
    elif action == "publish": publish(config)
    else: print("Verified WebView versions, changelog, app origins and Store signing pins")


if __name__ == "__main__": main()
