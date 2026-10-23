#!/usr/bin/env python3
"""Verify the APK signature, permanent certificate, package ID and version against the checked-in sources."""
import argparse,json,os,pathlib,re,subprocess
root=pathlib.Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser();p.add_argument('apk',type=pathlib.Path);args=p.parse_args()
sdk=pathlib.Path(os.environ.get('ANDROID_HOME',pathlib.Path.home()/'Library/Android/sdk'))
tools=sdk/'build-tools/35.0.0'
cert=subprocess.check_output([str(tools/'apksigner'),'verify','--verbose','--print-certs',str(args.apk)],text=True)
expected=(root/'android/release-certificate.sha256').read_text().strip()
actual=re.findall(r'^Signer #\d+ certificate SHA-256 digest: ([a-f0-9]+)$',cert,re.M)
if actual != [expected]:raise SystemExit('APK signing certificate does not match the permanent store key')
if 'Verified using v2 scheme (APK Signature Scheme v2): true' not in cert and 'Verified using v3 scheme (APK Signature Scheme v3): true' not in cert:raise SystemExit('APK must use v2/v3 signing')
metadata=subprocess.check_output([str(tools/'aapt2'),'dump','badging',str(args.apk)],text=True)
identity=re.search(r"package: name='([^']+)' versionCode='(\d+)' versionName='([^']+)'",metadata)
gradle=(root/'android/app/build.gradle.kts').read_text()
version=re.search(r'versionName = "([^"]+)"',gradle)[1];code=re.search(r'versionCode = (\d+)',gradle)[1]
if not identity or tuple(identity.groups()) != ('dev.jeeth.privatestore',code,version):raise SystemExit('APK package/version does not match the release source')
# Verify the compiled APK, so a catalog addition cannot silently become invisible
# to PackageManager on Android 11+. The store's own package is always visible.
manifest=subprocess.check_output([str(tools/'aapt2'),'dump','xmltree',str(args.apk),'--file','AndroidManifest.xml'],text=True)
query_block=re.search(r'^([ \t]*)E: queries.*\n((?:\1[ \t]+.*\n)*)',manifest,re.M)
visible=set(re.findall(r'android:name[^=]*=\"([^\"]+)\"',query_block[2])) if query_block else set()
required={app['packageName'] for app in json.loads((root/'config/apps.json').read_text())}-{'dev.jeeth.privatestore'}
missing=required-visible
if missing:raise SystemExit('APK cannot detect installed catalog apps; add <queries><package> declarations for: '+', '.join(sorted(missing)))
print('Verified catalog package visibility: '+', '.join(sorted(required)))
print('Verified signed Private Store '+version+' / code '+code+' / certificate '+expected)
