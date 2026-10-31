#!/usr/bin/env python3
"""Require Android package visibility for every app in the shipped store catalog."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[1]
ANDROID_NAME = "{http://schemas.android.com/apk/res/android}name"


def required_packages(catalog):
    return {app["packageName"] for app in catalog if app.get("packageName")} - {"dev.jeeth.privatestore"}


def source_packages(manifest):
    return {node.get(ANDROID_NAME) for node in ET.fromstring(manifest).findall("queries/package")}


def compiled_packages(manifest):
    # aapt2's indented XML tree: only package declarations inside <queries> count.
    queries_indent = None
    package_indent = None
    visible = set()
    for line in manifest.splitlines():
        indent = len(line) - len(line.lstrip())
        if line.lstrip().startswith("E:"):
            if queries_indent is not None and indent <= queries_indent:
                queries_indent = None
            package_indent = None
            if line.lstrip().startswith("E: queries "):
                queries_indent = indent
            elif queries_indent is not None and line.lstrip().startswith("E: package "):
                package_indent = indent
        elif package_indent is not None and indent > package_indent:
            match = re.search(r'A: (?:android|http://schemas\.android\.com/apk/res/android):name\([^)]*\)="([^"]+)"', line)
            if match:
                visible.add(match[1])
    return visible


def require_visible(required, visible):
    missing = required - visible
    if missing:
        raise ValueError("APK cannot detect installed catalog apps; add <queries><package> declarations for: "
                         + ", ".join(sorted(missing)))


def verify_apk(apk, required):
    sdk = Path(os.environ.get("ANDROID_HOME", Path.home() / "Library/Android/sdk"))
    manifest = subprocess.check_output([str(sdk / "build-tools/35.0.0/aapt2"), "dump", "xmltree",
                                        str(apk), "--file", "AndroidManifest.xml"], text=True)
    require_visible(required, compiled_packages(manifest))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apk", type=Path, help="Also check the compiled APK (debug or release)")
    args = parser.parse_args()
    required = required_packages(json.loads((ROOT / "config/apps.json").read_text()))
    require_visible(required, source_packages((ROOT / "android/app/src/main/AndroidManifest.xml").read_text()))
    if args.apk:
        verify_apk(args.apk, required)
    print("Verified catalog package visibility: " + ", ".join(sorted(required)))


if __name__ == "__main__":
    main()
