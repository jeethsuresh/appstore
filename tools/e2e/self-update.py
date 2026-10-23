#!/usr/bin/env python3
"""Replace the running .testbed app through its verified PackageInstaller path.

The emulator and .testbed data must be disposable. Production apps and credentials
are untouched. Android confirmation is approved only for this named test update.
"""
import argparse, atexit, hashlib, json, os, pathlib, re, shutil, subprocess, tempfile, time, xml.etree.ElementTree as ET

ROOT = pathlib.Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser()
p.add_argument('--serial', required=True)
p.add_argument('--work-dir', type=pathlib.Path)
args = p.parse_args()
if not re.fullmatch(r'emulator-\d+', args.serial):
    raise SystemExit('Use a disposable emulator')
work = args.work_dir or pathlib.Path(tempfile.mkdtemp(prefix='store-self-update-'))
work.mkdir(parents=True, exist_ok=True)
sdk = pathlib.Path(os.environ.get('ANDROID_HOME', pathlib.Path.home()/'Library/Android/sdk'))
env = {**os.environ, 'ANDROID_HOME': str(sdk)}
if not env.get('JAVA_HOME'):
    raise SystemExit('Set JAVA_HOME to JDK 17+')
package = 'dev.jeeth.privatestore.testbed'
adb_base = [str(sdk/'platform-tools/adb'), '-s', args.serial]
def adb(*cmd, **kwargs):
    return subprocess.run([*adb_base, *cmd], check=True, capture_output=True, timeout=180, **kwargs)
def build(code):
    with (work/f'build-{code}.log').open('w') as log:
        subprocess.run([str(ROOT/'android/gradlew'), '--no-daemon', '-PstoreTestBuildType=e2e', f'-PstoreE2eVersionCode={code}', 'assembleE2e', 'assembleE2eAndroidTest'], cwd=ROOT/'android', env=env, stdout=log, stderr=log, check=True)
    target = work/f'store-{code}.apk'
    shutil.copyfile(ROOT/'android/app/build/outputs/apk/e2e/app-e2e.apk', target)
    return target

# A test-only trust anchor is required by the e2e manifest even for an offline test.
raw = ROOT/'android/app/build/generated/e2e-res/raw'
raw.mkdir(parents=True, exist_ok=True)
if not (raw/'integration_ca.pem').exists():
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(work/'ca.key'), '-out', str(raw/'integration_ca.pem'), '-days', '2', '-subj', '/CN=Isolated Private Store self-update CA'], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

print('Building isolated baseline and replacement APKs…', flush=True)
baseline = build(30000)
updated = build(30001)
adb('wait-for-device')
deadline=time.monotonic()+180
while adb('shell','getprop','sys.boot_completed').stdout.strip()!=b'1':
    if time.monotonic()>deadline: raise TimeoutError('Emulator did not finish booting')
    time.sleep(1)
print('Installing the disposable test baseline…', flush=True)
adb('install', '-r', '-d', str(baseline))
adb('install', '-r', str(ROOT/'android/app/build/outputs/apk/androidTest/e2e/app-e2e-androidTest.apk'))
adb('shell', 'pm', 'clear', package)
adb('shell', 'appops', 'set', package, 'REQUEST_INSTALL_PACKAGES', 'allow')
adb('shell', 'pm', 'grant', package, 'android.permission.POST_NOTIFICATIONS')
# The fixture credentials are deliberately fake. Keep the real worker queued by
# its normal battery constraint so it cannot send those fixtures to live IAM.
adb('shell', 'dumpsys', 'battery', 'unplug')
adb('shell', 'dumpsys', 'battery', 'set', 'level', '5')
def cleanup():
    try: adb('shell','pm','clear',package)
    finally: adb('shell','dumpsys','battery','reset')
atexit.register(cleanup)
adb('shell', 'run-as', package, 'mkdir', '-p', 'files')
adb('shell', 'run-as', package, 'sh', '-c', "'cat > files/self-update.apk'", input=updated.read_bytes())
test_base = ['shell', 'am', 'instrument', '-w', '-e', 'selfUpdateTest', 'true', '-e', 'class']
print('Committing the verified self-update through the running store…', flush=True)
result = adb(*test_base, 'dev.jeeth.privatestore.SelfUpdateTest#beginVerifiedSelfUpdate', package+'.test/androidx.test.runner.AndroidJUnitRunner')
(work/'begin.log').write_bytes(result.stdout+result.stderr)
if b'FAILURES' in result.stdout or b'AssertionError' in result.stdout:
    raise RuntimeError('Self-update preparation failed; inspect begin.log')

deadline = time.monotonic()+300
approved = False
scan_notice = False
while time.monotonic() < deadline:
    info = adb('shell', 'dumpsys', 'package', package).stdout.decode()
    if re.search(r'versionCode=30001\b', info):
        break
    # Read Android's actual installer dialog before approving this test APK.
    try:
        adb('shell', 'uiautomator', 'dump', '/sdcard/self-update-window.xml')
    except subprocess.CalledProcessError:
        time.sleep(1)
        continue
    xml = adb('exec-out', 'cat', '/sdcard/self-update-window.xml').stdout
    try:
        nodes = list(ET.fromstring(xml).iter('node'))
        if not scan_notice and any(n.get('package')=='com.android.vending' and n.get('text')=='Private Store' for n in nodes):
            print('Approve the Play Protect prompt for this locally built test APK on the disposable emulator.',flush=True)
            scan_notice=True
        installer = [n for n in nodes if n.get('package') in ('com.android.packageinstaller', 'com.google.android.packageinstaller')]
        if installer and any('Private Store' in n.get('text','') for n in installer):
            buttons = [n for n in installer if n.get('text','').lower() in ('update', 'install') and n.get('enabled')=='true']
            if len(buttons)==1:
                x1,y1,x2,y2=map(int,re.findall(r'\d+',buttons[0].get('bounds','')))
                adb('shell','input','tap',str((x1+x2)//2),str((y1+y2)//2));approved=True
    except ET.ParseError:
        pass
    time.sleep(1)
else:
    raise TimeoutError('Android did not complete the self-update; inspect the emulator')

time.sleep(3)
print('Verifying encrypted state and scheduling in the replacement process…', flush=True)
result=adb(*test_base, 'dev.jeeth.privatestore.SelfUpdateTest#verifyReplacementPreservesPrivateStateAndSchedule', package+'.test/androidx.test.runner.AndroidJUnitRunner')
(work/'verify.log').write_bytes(result.stdout+result.stderr)
if b'OK (1 test)' not in result.stdout:
    raise RuntimeError('Self-update preservation failed; inspect verify.log')
report={'passed':True,'package':package,'fromVersionCode':30000,'toVersionCode':30001,'androidConfirmationApproved':approved,'signedApkSHA256':hashlib.sha256(updated.read_bytes()).hexdigest(),'preserved':['encrypted device login','encrypted broker tokens and proof','Android account','automatic update preference','last check','unique periodic work'],'completedInstallMarkerCleared':True,'processReplaced':True}
(work/'report.json').write_text(json.dumps(report,indent=2)+'\n')
print('PASS: verified self-install replaced the running store and preserved encrypted login, broker account, settings and periodic updates.')
