#!/usr/bin/env python3
"""Exercise real HTTPS enrollment, both APK downloads, Android install, worker update and revocation.

Uses an isolated .testbed Android package and temporary CA trusted only by the e2e variant.
No server is deployed. Approve any Android/Play Protect prompts on the emulator.
"""
import argparse, contextlib, hashlib, json, os, pathlib, re, secrets, socket, ssl, subprocess, sys, tempfile, time, urllib.request, shutil
ROOT=pathlib.Path(__file__).resolve().parents[2]
p=argparse.ArgumentParser();p.add_argument('--serial',default='emulator-5554');p.add_argument('--work-dir',type=pathlib.Path);p.add_argument('--reset-test-daybreak',action='store_true',help='Remove Daybreak from the disposable emulator before the test');args=p.parse_args()
work=args.work_dir or pathlib.Path(tempfile.mkdtemp(prefix='private-store-e2e-'))
work.mkdir(parents=True,exist_ok=True);os.chmod(work,0o700)
marker=work/'.private-store-e2e'
if (work/'data').exists():
    if not marker.exists():raise SystemExit('Refusing to reset an unmarked existing data directory; choose a fresh --work-dir.')
    shutil.rmtree(work/'data')
marker.touch(mode=0o600)
sdk=pathlib.Path(os.environ.get('ANDROID_HOME',pathlib.Path.home()/'Library/Android/sdk'))
java=os.environ.get('JAVA_HOME','')
if not java:raise SystemExit('Set JAVA_HOME to a JDK 17+ directory')
env={**os.environ,'ANDROID_HOME':str(sdk),'JAVA_HOME':java}
def run(command,**kwargs):return subprocess.run([str(v) for v in command],check=True,env=env,text=True,**kwargs)
def adb(*command,capture=True):return run([sdk/'platform-tools/adb','-s',args.serial,*command],capture_output=capture).stdout if capture else run([sdk/'platform-tools/adb','-s',args.serial,*command])
if not args.serial.startswith('emulator-'):raise SystemExit('This test is restricted to disposable emulators')
existing=adb('shell','pm','list','packages','dev.jeeth.local_weather').strip()
if existing:
    if not args.reset_test_daybreak:raise SystemExit('Daybreak exists on the emulator. Use a clean emulator or --reset-test-daybreak only if its data is disposable.')
    adb('uninstall','dev.jeeth.local_weather')
# Dedicated test CA: no trust changes to the emulator or to production/debug app variants.
run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',work/'ca.key','-out',work/'ca.pem','-days','2','-subj','/CN=Private Store isolated integration CA','-addext','basicConstraints=critical,CA:TRUE','-addext','keyUsage=critical,keyCertSign,cRLSign'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
run(['openssl','req','-newkey','rsa:2048','-nodes','-keyout',work/'server.key','-out',work/'server.csr','-subj','/CN=localhost'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
(work/'server.ext').write_text('subjectAltName=DNS:localhost,IP:127.0.0.1,IP:10.0.2.2\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n')
run(['openssl','x509','-req','-in',work/'server.csr','-CA',work/'ca.pem','-CAkey',work/'ca.key','-CAcreateserial','-out',work/'server.pem','-days','2','-extfile',work/'server.ext'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
resources=ROOT/'android/app/build/generated/e2e-res/raw';resources.mkdir(parents=True,exist_ok=True);(resources/'integration_ca.pem').write_bytes((work/'ca.pem').read_bytes())
with (work/'build.log').open('w') as log:
    print('Building backend and isolated e2e client…',flush=True)
    run(['go','build','-o',work/'store','./cmd/store'],cwd=ROOT/'backend',stdout=log,stderr=log)
    run([ROOT/'android/gradlew','--no-daemon','-PstoreTestBuildType=e2e','assembleE2e','assembleE2eAndroidTest'],cwd=ROOT/'android',stdout=log,stderr=log)
with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
admin=secrets.token_hex(32)
serverEnv={**env,'ADMIN_TOKEN':admin,'WEBHOOK_SECRET':secrets.token_hex(32),'LISTEN_ADDR':f'127.0.0.1:{port}','DATA_DIR':str(work/'data'),'APPS_CONFIG':str(ROOT/'config/apps.json'),'POLL_INTERVAL':'0s','TLS_CERT_FILE':str(work/'server.pem'),'TLS_KEY_FILE':str(work/'server.key'),'AAPT':str(sdk/'build-tools/35.0.0/aapt2'),'APKSIGNER':str(sdk/'build-tools/35.0.0/apksigner')}
# Reuse Git's existing helper if available, without writing or displaying a token.
credential=subprocess.run(['git','credential','fill'],input='protocol=https\nhost=github.com\n\n',capture_output=True,text=True,env={**env,'GIT_TERMINAL_PROMPT':'0'})
fields=dict(line.split('=',1) for line in credential.stdout.splitlines() if '=' in line)
if fields.get('password'):serverEnv['GITHUB_TOKEN']=fields['password']
context=ssl.create_default_context(cafile=str(work/'ca.pem'))
base=f'https://127.0.0.1:{port}'
def api(path,body=None,method='POST',authenticated=True):
    request=urllib.request.Request(base+path,data=json.dumps(body).encode() if body is not None else None,method=method,headers={'Content-Type':'application/json',**({'Authorization':'Bearer '+admin} if authenticated else {})})
    with urllib.request.urlopen(request,context=context,timeout=300) as response:return json.load(response) if response.status!=204 else None
serverLog=(work/'server.log').open('w');server=subprocess.Popen([str(work/'store')],env=serverEnv,stdout=serverLog,stderr=serverLog)
try:
    for _ in range(100):
        try:api('/healthz',method='GET',authenticated=False);break
        except OSError:time.sleep(.1)
    else:raise RuntimeError('HTTPS backend did not start')
    print('Ingesting real signed Daybreak 1.4.1 and Spacetime releases…',flush=True)
    baseline=api('/admin/sync/daybreak?tag=v1.4.1');spacetime=api('/admin/sync/spacetime')
    if baseline['versionCode']!=6:raise RuntimeError('Unexpected baseline release; update this fixture intentionally')
    invite=api('/admin/invites',{'label':'Isolated distribution E2E','uses':1,'expiresInHours':1})['invite']
    adb('install','-r',str(ROOT/'android/app/build/outputs/apk/e2e/app-e2e.apk'))
    adb('install','-r',str(ROOT/'android/app/build/outputs/apk/androidTest/e2e/app-e2e-androidTest.apk'))
    package='dev.jeeth.privatestore.testbed'
    adb('shell','pm','clear',package)
    adb('shell','appops','set',package,'REQUEST_INSTALL_PACKAGES','allow')
    adb('shell','pm','grant',package,'android.permission.POST_NOTIFICATIONS')
    runId=secrets.token_hex(8)
    instrumentLog=(work/'instrumentation.log').open('w')
    command=[sdk/'platform-tools/adb','-s',args.serial,'shell','am','instrument','-w','-e','class','dev.jeeth.privatestore.DistributionE2ETest','-e','distributionE2E','true','-e','runId',runId,'-e','serverUrl',f'https://10.0.2.2:{port}','-e','invite',invite,package+'.test/androidx.test.runner.AndroidJUnitRunner']
    instrument=subprocess.Popen([str(v) for v in command],stdout=instrumentLog,stderr=instrumentLog,env=env)
    seen=set();latest=None;deadline=time.time()+600
    try:
        while instrument.poll() is None and time.time()<deadline:
            logs=adb('logcat','-d','-s','StoreE2E:I')
            for marker in ['ENROLLED_THROUGH_UI','SPACETIME_DOWNLOAD_VERIFIED','READY_FOR_INITIAL_INSTALL','BASELINE_INSTALLED','BACKGROUND_UPDATE_ENQUEUED','BACKGROUND_UPDATE_INSTALLED','DEVICE_REVOKED']:
                if runId+':'+marker in logs and marker not in seen:
                    seen.add(marker);print(marker,flush=True)
                    if marker=='READY_FOR_INITIAL_INSTALL':print('Approve the Android installation/Play Protect prompts on the emulator.',flush=True)
                    if marker=='BASELINE_INSTALLED':
                        print('Ingesting current Daybreak release for a real worker upgrade…',flush=True);latest=api('/admin/sync/daybreak')
            time.sleep(1)
        if instrument.poll() is None:raise TimeoutError('Instrumentation did not finish')
    finally:
        if instrument.poll() is None:instrument.terminate();instrument.wait(timeout=10)
        instrumentLog.close()
    finalLogs=adb('logcat','-d','-s','StoreE2E:I')
    for marker in ['ENROLLED_THROUGH_UI','SPACETIME_DOWNLOAD_VERIFIED','READY_FOR_INITIAL_INSTALL','BASELINE_INSTALLED','BACKGROUND_UPDATE_ENQUEUED','BACKGROUND_UPDATE_INSTALLED','DEVICE_REVOKED']:
        if runId+':'+marker in finalLogs:seen.add(marker)
    output=(work/'instrumentation.log').read_text()
    if 'OK (1 test)' not in output:raise RuntimeError('Android end-to-end test failed: '+output[-5000:])
    info=adb('shell','dumpsys','package','dev.jeeth.local_weather')
    expected='installerPackageName='+package
    if expected not in info:raise AssertionError('Store is not installer of record')
    devices=api('/admin/devices',method='GET')
    if not devices or not all(d['revoked'] for d in devices):raise AssertionError('Device revocation not recorded')
    report={'passed':True,'baseline':baseline,'updated':latest,'spacetime':spacetime,'stages':sorted(seen),'installerPackage':package,'revocationVerified':True,'tls':'isolated test CA; production trust unchanged'}
    (work/'report.json').write_text(json.dumps(report,indent=2)+'\n')
    print('PASS: HTTPS enrollment, both signed APK downloads, initial install, background worker upgrade, and revocation.',flush=True)
finally:
    server.terminate()
    try:server.wait(timeout=20)
    except subprocess.TimeoutExpired:server.kill();server.wait()
    serverLog.close()
