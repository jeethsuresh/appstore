#!/usr/bin/env python3
"""Install the actual signed launcher through the store and verify installed-version detection.

Uses local HTTPS, the isolated .testbed store and a disposable emulator. Approve the
normal Android installation prompt. Production accounts and servers are not used.
"""
import argparse, datetime, json, os, pathlib, re, secrets, shutil, socket, ssl, subprocess, tempfile, time, urllib.request, xml.etree.ElementTree as ET

ROOT = pathlib.Path(__file__).resolve().parents[2]
p = argparse.ArgumentParser()
p.add_argument('--serial', required=True)
p.add_argument('--work-dir', type=pathlib.Path)
p.add_argument('--reset-test-launcher', action='store_true')
args = p.parse_args()
if not args.serial.startswith('emulator-'):
    raise SystemExit('Only disposable emulators are supported')
work = args.work_dir or pathlib.Path(tempfile.mkdtemp(prefix='launcher-install-'))
work.mkdir(parents=True, exist_ok=True)
os.chmod(work, 0o700)
if (work/'data').exists():
    raise SystemExit('Choose a fresh work directory; existing test data is preserved')
sdk = pathlib.Path(os.environ.get('ANDROID_HOME', pathlib.Path.home()/'Library/Android/sdk'))
if not os.environ.get('JAVA_HOME'):
    raise SystemExit('Set JAVA_HOME to JDK 17+')
env = {**os.environ, 'ANDROID_HOME': str(sdk)}

def run(command, **kw):
    return subprocess.run([str(v) for v in command], env=env, check=True, text=True, **kw)

def adb(*command):
    return run([sdk/'platform-tools/adb', '-s', args.serial, *command], capture_output=True).stdout

def approve_install_prompt():
    # Only approve observed system dialogs for this known signed test APK.
    adb('shell', 'uiautomator', 'dump', '/sdcard/launcher-install-test.xml')
    tree = ET.fromstring(adb('shell', 'cat', '/sdcard/launcher-install-test.xml'))
    nodes = list(tree.iter('node'))
    if not any(n.get('text') == 'Still Launcher' for n in nodes):
        return
    for n in nodes:
        package, text = n.get('package'), n.get('text', '')
        approved = package == 'com.google.android.packageinstaller' and text in ['Install', 'Update', 'Done']
        details = package == 'com.android.vending' and text == 'More details'
        without_scan = package == 'com.android.vending' and 'Install without scanning' in text
        if not (approved or details or without_scan):
            continue
        x1, y1, x2, y2 = map(int, re.findall(r'\d+', n.get('bounds')))
        adb('shell', 'input', 'tap', str((x1+x2)//2), str(y2-12 if without_scan else (y1+y2)//2))
        print('Approved normal Android prompt:', text.splitlines()[-1], flush=True)
        return

package = 'dev.jeeth.privatestore.testbed'
launcher = 'app.still.launcher'
if adb('shell', 'pm', 'list', 'packages', launcher).strip():
    if not args.reset_test_launcher:
        raise SystemExit('Launcher already exists; use a clean emulator or explicitly --reset-test-launcher')
    adb('uninstall', launcher)
apps = [a for a in json.loads((ROOT/'config/apps.json').read_text()) if a['id'] == 'launcher']
assert len(apps) == 1 and apps[0]['packageName'] == launcher
(work/'apps.json').write_text(json.dumps(apps))
for command in [
    ['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', work/'ca.key', '-out', work/'ca.pem', '-days', '2', '-subj', '/CN=Launcher installation test CA', '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign'],
    ['openssl', 'req', '-newkey', 'rsa:2048', '-nodes', '-keyout', work/'server.key', '-out', work/'server.csr', '-subj', '/CN=localhost'],
]:
    run(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
(work/'server.ext').write_text('subjectAltName=DNS:localhost,IP:127.0.0.1,IP:10.0.2.2\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n')
run(['openssl', 'x509', '-req', '-in', work/'server.csr', '-CA', work/'ca.pem', '-CAkey', work/'ca.key', '-CAcreateserial', '-out', work/'server.pem', '-days', '2', '-extfile', work/'server.ext'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
raw = ROOT/'android/app/build/generated/e2e-res/raw'
raw.mkdir(parents=True, exist_ok=True)
shutil.copyfile(work/'ca.pem', raw/'integration_ca.pem')
print('Building isolated store and local backend…', flush=True)
with (work/'build.log').open('w') as log:
    run(['go', 'build', '-o', work/'store', './cmd/store'], cwd=ROOT/'backend', stdout=log, stderr=log)
    run([ROOT/'android/gradlew', '--no-daemon', '-PstoreTestBuildType=e2e', 'assembleE2e', 'assembleE2eAndroidTest'], cwd=ROOT/'android', stdout=log, stderr=log)
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    port = sock.getsockname()[1]
admin = secrets.token_hex(32)
server_env = {**env, 'ADMIN_TOKEN': admin, 'WEBHOOK_SECRET': secrets.token_hex(32), 'LISTEN_ADDR': f'127.0.0.1:{port}', 'DATA_DIR': str(work/'data'), 'APPS_CONFIG': str(work/'apps.json'), 'POLL_INTERVAL': '0s', 'TLS_CERT_FILE': str(work/'server.pem'), 'TLS_KEY_FILE': str(work/'server.key'), 'AAPT': str(sdk/'build-tools/35.0.0/aapt2'), 'APKSIGNER': str(sdk/'build-tools/35.0.0/apksigner')}
credential = subprocess.run(['git', 'credential', 'fill'], input='protocol=https\nhost=github.com\n\n', capture_output=True, text=True, env={**env, 'GIT_TERMINAL_PROMPT': '0'})
fields = dict(line.split('=', 1) for line in credential.stdout.splitlines() if '=' in line)
if fields.get('password'):
    server_env['GITHUB_TOKEN'] = fields['password']
tls = ssl.create_default_context(cafile=str(work/'ca.pem'))

def api(path, body=None, method='POST'):
    req = urllib.request.Request(f'https://127.0.0.1:{port}'+path, data=json.dumps(body).encode() if body is not None else None, method=method, headers={'Content-Type': 'application/json', 'Authorization': 'Bearer '+admin})
    with urllib.request.urlopen(req, context=tls, timeout=300) as response:
        return json.load(response) if response.status != 204 else None

log = (work/'server.log').open('w')
server = subprocess.Popen([str(work/'store')], env=server_env, stdout=log, stderr=log)
try:
    for _ in range(100):
        try:
            api('/healthz', method='GET')
            break
        except OSError:
            time.sleep(.1)
    else:
        raise RuntimeError('Local HTTPS backend did not start')
    release = api('/admin/sync/launcher')
    print('Verified signed launcher release:', release['versionName'], 'code', release['versionCode'], flush=True)
    invite = api('/admin/invites', {'label': 'Disposable launcher regression', 'uses': 1, 'expiresInHours': 1})['invite']
    # Reinstall the isolated store so Android also reloads its per-run test CA.
    for test_package in [package+'.test', package]:
        if adb('shell', 'pm', 'list', 'packages', test_package).strip():
            adb('uninstall', test_package)
    adb('install', '-r', str(ROOT/'android/app/build/outputs/apk/e2e/app-e2e.apk'))
    adb('install', '-r', str(ROOT/'android/app/build/outputs/apk/androidTest/e2e/app-e2e-androidTest.apk'))
    adb('shell', 'pm', 'clear', package)
    adb('shell', 'appops', 'set', package, 'REQUEST_INSTALL_PACKAGES', 'allow')
    adb('shell', 'pm', 'grant', package, 'android.permission.POST_NOTIFICATIONS')
    # A busy emulator can lag the host clock. Keep TLS validation enabled and
    # wait until the temporary server certificate is valid on the device.
    start = run(['openssl', 'x509', '-in', work/'server.pem', '-noout', '-startdate'], capture_output=True).stdout.strip().split('=', 1)[1]
    not_before = datetime.datetime.strptime(start, '%b %d %H:%M:%S %Y %Z').replace(tzinfo=datetime.timezone.utc).timestamp()
    while True:
        remaining = not_before+2-int(adb('shell', 'date', '+%s').strip())
        if remaining <= 0:
            break
        if remaining > 120:
            raise RuntimeError('Emulator clock is over two minutes behind; correct it before running this HTTPS test')
        print('Waiting for test certificate validity on emulator:', int(remaining), 'seconds', flush=True)
        time.sleep(min(remaining, 10))
    print('Installing through Private Store. Approve the normal Android prompt on', args.serial, flush=True)
    with (work/'instrumentation.log').open('w') as output:
        result = subprocess.Popen([str(sdk/'platform-tools/adb'), '-s', args.serial, 'shell', 'am', 'instrument', '-w', '-e', 'class', 'dev.jeeth.privatestore.LauncherInstallationTest', '-e', 'launcherInstallE2E', 'true', '-e', 'serverUrl', f'https://10.0.2.2:{port}', '-e', 'invite', invite, package+'.test/androidx.test.runner.AndroidJUnitRunner'], env=env, stdout=output, stderr=output)
        try:
            deadline = time.monotonic()+300
            while result.poll() is None and time.monotonic() < deadline:
                approve_install_prompt()
                time.sleep(1)
            if result.poll() is None:
                raise TimeoutError('Launcher instrumentation timed out')
        finally:
            if result.poll() is None:
                result.terminate()
                result.wait(timeout=10)
    text = (work/'instrumentation.log').read_text()
    info = adb('shell', 'dumpsys', 'package', launcher)
    receipt = {'passed': result.returncode == 0 and 'OK (1 test)' in text, 'release': release, 'installerPackage': package, 'installerOfRecord': 'installerPackageName='+package in info, 'tls': 'local test CA; production trust unchanged'}
    (work/'report.json').write_text(json.dumps(receipt, indent=2)+'\n')
    if not receipt['passed']:
        raise RuntimeError('Launcher regression failed: '+text[-6000:])
    assert receipt['installerOfRecord']
    assert all(d['revoked'] for d in api('/admin/devices', method='GET'))
    print('PASS: installed version, current UI after refresh/recreation, duplicate-install rejection, worker skips current launcher, and credential revocation.', flush=True)
finally:
    try:
        adb('shell', 'am', 'force-stop', package)
    finally:
        server.terminate()
        try:
            server.wait(timeout=15)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait()
        log.close()
