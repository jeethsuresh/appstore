#!/usr/bin/env python3
"""Build the signed client using its locally protected signing configuration."""
import json,os,pathlib,subprocess,sys
root=pathlib.Path(__file__).resolve().parents[1]
p=root/'.signing/signing.json'
if not p.exists():raise SystemExit('Run scripts/create-signing-key.py or supply the store signing configuration first.')
c=json.loads(p.read_text())
env={**os.environ,'STORE_KEYSTORE':c['storeFile'],'STORE_STORE_PASSWORD':c['storePassword'],'STORE_KEY_ALIAS':c['keyAlias'],'STORE_KEY_PASSWORD':c['keyPassword']}
subprocess.run([str(root/'android/gradlew'), '--no-daemon', * (sys.argv[1:] or ['assembleRelease','testDebugUnitTest','lintRelease'])],cwd=root/'android',env=env,check=True)
