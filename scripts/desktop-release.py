#!/usr/bin/env python3
"""Validate, stage and atomically publish Private Store desktop releases."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
REPO = "jeethsuresh/appstore"
TARGETS = {(platform, arch) for platform in ("macos", "windows", "linux") for arch in ("x64", "arm64")}


def validate():
    package = json.loads((ROOT / "desktop/package.json").read_text())
    changelog = json.loads((ROOT / "desktop/changelog.json").read_text())
    if not re.fullmatch(r"(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)", package["version"]):
        raise ValueError("desktop version must be stable X.Y.Z")
    if type(package.get("versionCode")) is not int or not 0 < package["versionCode"] <= 2**53 - 1:
        raise ValueError("desktop versionCode must be a positive safe integer")
    if (changelog.get("schemaVersion"), changelog.get("version"), changelog.get("versionCode")) != (1, package["version"], package["versionCode"]):
        raise ValueError("desktop changelog must match the declared version and versionCode")
    changes = changelog.get("changes")
    if not isinstance(changes, list) or not 1 <= len(changes) <= 50 or any(not isinstance(c, str) or not c.strip() or len(c.encode()) > 2000 or any(ord(x) < 32 for x in c) for c in changes):
        raise ValueError("desktop changes must be nonempty plain-text strings")
    return package, changelog


def sha256(file):
    with file.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def stage(directory):
    package, changelog = validate()
    artifacts = []
    found = set()
    for file in sorted(directory.iterdir()):
        match = re.fullmatch(r"PrivateStore-" + re.escape(package["version"]) + r"-(mac|win|linux)-(x64|x86_64|arm64|aarch64)\.(zip|AppImage)", file.name)
        if not match:
            continue
        platform = {"mac": "macos", "win": "windows", "linux": "linux"}[match[1]]
        arch = {"x86_64": "x64", "aarch64": "arm64"}.get(match[2], match[2])
        if (platform, arch) in found or (match[3] == "AppImage") != (platform == "linux"):
            raise ValueError("duplicate or incompatible desktop package")
        found.add((platform, arch))
        entry = {"macos": "Private Store.app", "windows": "Private Store.exe", "linux": file.name}[platform]
        artifacts.append(dict(platform=platform, arch=arch, file=file.name, entry=entry, sha256=sha256(file), size=file.stat().st_size))
    if found != TARGETS:
        raise ValueError(f"all six desktop targets must be present; missing {TARGETS - found}")
    manifest = {**changelog, "artifacts": artifacts}
    if len((json.dumps(manifest, indent=2) + "\n").encode()) > 64 * 1024:
        raise ValueError("desktop release manifest exceeds the ingestion size limit")
    (directory / "desktop-release.json").write_text(json.dumps(manifest, indent=2) + "\n")
    (directory / "desktop-changelog.json").write_text(json.dumps(changelog, indent=2) + "\n")
    files = [directory / a["file"] for a in artifacts] + [directory / "desktop-release.json", directory / "desktop-changelog.json"]
    (directory / "SHA256SUMS").write_text("".join(f"{sha256(file)}  {file.name}\n" for file in files))
    return package, manifest


def api(path, method="GET", value=None, binary=None):
    url = path if path.startswith("https://") else "https://api.github.com" + path
    if not (url.startswith("https://api.github.com/") or url.startswith("https://uploads.github.com/")):
        raise ValueError("untrusted release API URL")
    headers = {"Authorization": "Bearer " + os.environ["GH_TOKEN"], "Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "private-store-release"}
    data = None
    if value is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(value).encode()
    if binary is not None:
        headers["Content-Type"] = "application/octet-stream"
        data = binary
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers, data=data, method=method), timeout=600) as response:
        return json.load(response)


def publish(directory):
    if os.environ.get("GITHUB_REF") != "refs/heads/main" or os.environ.get("GITHUB_EVENT_NAME") not in ("push", "workflow_dispatch"):
        raise ValueError("desktop publication is restricted to main")
    package, manifest = stage(directory)
    tag = "desktop-v" + package["version"]
    try:
        existing = api(f"/repos/{REPO}/releases/tags/{tag}")
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise
    else:
        if existing.get("draft"):
            raise ValueError("an interrupted draft already exists; inspect it before retrying")
        print(f"{tag} already published; increase desktop version and versionCode for a new release")
        return
    # Reject decreasing versions even when tags have a different creation order.
    for release in api(f"/repos/{REPO}/releases?per_page=100"):
        if release["tag_name"].startswith("desktop-v"):
            match = re.fullmatch(r"desktop-v(\d+)\.(\d+)\.(\d+)", release["tag_name"])
            if match and tuple(map(int, match.groups())) >= tuple(map(int, package["version"].split("."))):
                raise ValueError("desktop stable version must increase")
            for asset in release["assets"]:
                if asset["name"] == "desktop-release.json":
                    with urllib.request.urlopen(asset["browser_download_url"], timeout=60) as response:
                        previous = json.load(response)
                    if previous["versionCode"] >= package["versionCode"]:
                        raise ValueError("desktop versionCode must increase")
            break
    release = api(f"/repos/{REPO}/releases", "POST", {"tag_name": tag, "target_commitish": os.environ["GITHUB_SHA"], "name": "Private Store desktop " + package["version"], "body": "\n".join("- " + c for c in manifest["changes"]), "draft": True, "prerelease": False, "make_latest": "false"})
    files = [directory / a["file"] for a in manifest["artifacts"]] + [directory / "desktop-release.json", directory / "desktop-changelog.json", directory / "SHA256SUMS"]
    upload = release["upload_url"].split("{")[0]
    for file in files:
        asset = api(upload + "?" + urllib.parse.urlencode({"name": file.name}), "POST", binary=file.read_bytes())
        if asset["size"] != file.stat().st_size or asset.get("digest") != "sha256:" + sha256(file):
            raise ValueError("GitHub upload checksum/size verification failed: " + file.name)
    api(f"/repos/{REPO}/releases/{release['id']}", "PATCH", {"draft": False, "make_latest": "false"})
    print("Published " + tag + " with six verified platform builds")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["validate", "stage", "publish"])
    parser.add_argument("--directory", type=Path, default=ROOT / "desktop/dist")
    args = parser.parse_args()
    if args.command == "validate":
        validate()
    elif args.command == "stage":
        stage(args.directory)
    else:
        publish(args.directory)
