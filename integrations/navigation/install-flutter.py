#!/usr/bin/env python3
"""Copy the shared components; see ANDROID_NAVIGATION.md for routing integration."""
from pathlib import Path
import argparse
import re

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('project', type=Path, help='Flutter project containing pubspec.yaml')
args = parser.parse_args()
project = args.project.resolve()
name = re.search(r'^name:\s*(\S+)', (project / 'pubspec.yaml').read_text(), re.M)
if name is None:
    parser.error('pubspec.yaml must declare the package name')
source = Path(__file__).resolve().parent
native = project / 'android/app/src/main/java/cool/gravitas/navigation'
native.mkdir(parents=True, exist_ok=True)
for filename in ('GravitasEdgeNavigation.java', 'GravitasFlutterActivity.kt'):
    (native / filename).write_text((source / filename).read_text())
(project / 'lib/gravitas_navigation.dart').write_text((source / 'gravitas_navigation.dart').read_text())
(project / 'test').mkdir(exist_ok=True)
test = (source / 'gravitas_navigation_test.dart').read_text().replace(
    "'../lib/gravitas_navigation.dart'", f"'package:{name.group(1)}/gravitas_navigation.dart'")
(project / 'test/gravitas_navigation_test.dart').write_text(test)
print('Copied shared components. Integrate the activity, navigator, destinations, and account boundary per ANDROID_NAVIGATION.md.')
