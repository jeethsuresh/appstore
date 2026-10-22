#!/usr/bin/env python3
"""Test native PKCE against the deployed store and IAM using an isolated emulator package.

The supplied private env file must contain IAM_USERNAME and IAM_PASSWORD for an
existing test account. Passwords/cookies stay in the host harness, never in app
arguments, logs or APKs. No accounts are created and test device access is revoked.
"""
import argparse,html,http.cookiejar,http.server,json,os,pathlib,re,ssl,subprocess,tempfile,threading,urllib.error,urllib.parse,urllib.request
from html.parser import HTMLParser
ROOT=pathlib.Path(__file__).resolve().parents[2]
p=argparse.ArgumentParser();p.add_argument('--serial',default='emulator-5554');p.add_argument('--iam-login-env',type=pathlib.Path,required=True);p.add_argument('--work-dir',type=pathlib.Path);args=p.parse_args()
if not args.serial.startswith('emulator-'):raise SystemExit('Use a disposable emulator, not a physical phone.')
work=args.work_dir or pathlib.Path(tempfile.mkdtemp(prefix='native-gravitas-e2e-'));work.mkdir(parents=True,exist_ok=True);os.chmod(work,0o700)
creds=dict(line.split('=',1) for line in args.iam_login_env.read_text().splitlines() if '=' in line and not line.startswith('#'))
sdk=pathlib.Path(os.environ['ANDROID_HOME']);env=os.environ.copy()
def run(*cmd,**kwargs):return subprocess.run([str(v) for v in cmd],check=True,text=True,env=env,**kwargs)
def adb(*cmd):return run(sdk/'platform-tools/adb','-s',args.serial,*cmd,capture_output=True).stdout
for cmd in [
 ['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',work/'ca.key','-out',work/'ca.pem','-days','2','-subj','/CN=Native login isolated test CA','-addext','basicConstraints=critical,CA:TRUE','-addext','keyUsage=critical,keyCertSign,cRLSign'],
 ['openssl','req','-newkey','rsa:2048','-nodes','-keyout',work/'server.key','-out',work/'server.csr','-subj','/CN=localhost']]:run(*cmd,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
(work/'server.ext').write_text('subjectAltName=DNS:localhost,IP:127.0.0.1,IP:10.0.2.2\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n')
run('openssl','x509','-req','-in',work/'server.csr','-CA',work/'ca.pem','-CAkey',work/'ca.key','-CAcreateserial','-out',work/'server.pem','-days','2','-extfile',work/'server.ext',stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
res=ROOT/'android/app/build/generated/e2e-res/raw';res.mkdir(parents=True,exist_ok=True);(res/'integration_ca.pem').write_bytes((work/'ca.pem').read_bytes())
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args):return None
jar=http.cookiejar.CookieJar(policy=http.cookiejar.DefaultCookiePolicy(strict_ns_domain=http.cookiejar.DefaultCookiePolicy.DomainStrictNonDomain))
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar),NoRedirect())
class Forms(HTMLParser):
 def __init__(self):super().__init__();self.forms=[];self.current=None
 def handle_starttag(self,tag,attrs):
  a=dict(attrs)
  if tag=='form':self.current={'action':a.get('action',''),'hidden':{},'inputs':set()};self.forms.append(self.current)
  if tag=='input' and self.current is not None:
   name=a.get('name');self.current['inputs'].add(name)
   if a.get('type')=='hidden' and name:self.current['hidden'][name]=a.get('value','')
 def handle_endtag(self,tag):
  if tag=='form':self.current=None

def fetch(address,form=None):
 u=urllib.parse.urlsplit(address)
 if u.scheme!='https' or u.netloc!='gravitas.cool':raise RuntimeError('Unexpected identity endpoint')
 request=urllib.request.Request(address,data=urllib.parse.urlencode(form).encode() if form is not None else None,headers={'Content-Type':'application/x-www-form-urlencoded'} if form is not None else {})
 try:r=client.open(request,timeout=45)
 except urllib.error.HTTPError as e:r=e
 with r:return r.status,r.headers,r.read()
def authorize(address):
 u=urllib.parse.urlsplit(address);q=urllib.parse.parse_qs(u.query)
 assert u.scheme=='https' and u.netloc=='gravitas.cool' and u.path=='/oauth/start'
 assert q['scope']==['profile.name'] and q['codeChallengeMethod']==['S256'] and q['redirectURL']==['https://apps.gravitas.cool/auth/android/callback']
 for _ in range(18):
  if address.startswith('https://apps.gravitas.cool/auth/android/callback?'):return address
  status,headers,body=fetch(address)
  if status in (302,303):address=urllib.parse.urljoin(address,headers['Location']);continue
  assert status==200
  parser=Forms();parser.feed(body.decode())
  form=next((f for f in parser.forms if 'password' in f['inputs']),None)
  consent=next((f for f in parser.forms if f['action']=='/oauth/consent'),None)
  if form:
   assert urllib.parse.urljoin(address,form['action'])=='https://gravitas.cool/web/login'
   status,headers,_=fetch('https://gravitas.cool/web/login',{**form['hidden'],'username':creds['IAM_USERNAME'],'password':creds['IAM_PASSWORD']})
  elif consent:
   assert b'profile.name' in body and not any(s in body for s in [b'chats.read',b'chats.write',b'chats.list'])
   status,headers,_=fetch('https://gravitas.cool/oauth/consent',consent['hidden'])
  else:raise RuntimeError('Unexpected sign-in page')
  assert status in (302,303);address=urllib.parse.urljoin(address,headers['Location'])
 raise RuntimeError('Sign-in did not complete')
class Control(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_POST(self):
  try:
   assert self.path=='/authorize' and int(self.headers['Content-Length'])<16_384
   data=json.loads(self.rfile.read(int(self.headers['Content-Length'])));callback=authorize(data['authorizationUrl'])
   body=json.dumps({'callback':callback}).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(body)
  except Exception:
   self.send_error(500,'Identity test failed')
server=http.server.HTTPServer(('127.0.0.1',0),Control);tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(work/'server.pem',work/'server.key');server.socket=tls.wrap_socket(server.socket,server_side=True)
threading.Thread(target=server.serve_forever,daemon=True).start()
try:
 print('Building the isolated Android integration test…',flush=True)
 with (work/'build.log').open('w') as log:run(ROOT/'android/gradlew','--no-daemon','-PstoreTestBuildType=e2e','assembleE2e','assembleE2eAndroidTest',cwd=ROOT/'android',stdout=log,stderr=log)
 package='dev.jeeth.privatestore.testbed'
 adb('install','-r',ROOT/'android/app/build/outputs/apk/e2e/app-e2e.apk');adb('install','-r',ROOT/'android/app/build/outputs/apk/androidTest/e2e/app-e2e-androidTest.apk')
 adb('shell','pm','clear',package);adb('shell','pm','grant',package,'android.permission.POST_NOTIFICATIONS')
 result=adb('shell','am','instrument','-w','-e','class','dev.jeeth.privatestore.GravitasE2ETest','-e','nativeGravitasE2E','true','-e','controlUrl',f'https://10.0.2.2:{server.server_port}',package+'.test/androidx.test.runner.AndroidJUnitRunner')
 (work/'instrumentation.log').write_text(result)
 if 'OK (1 test)' not in result:raise RuntimeError('Native integration failed; inspect instrumentation.log')
 (work/'report.json').write_text(json.dumps({'passed':True,'realGravitasPKCE':True,'verifierRemainedOnDevice':True,'cancellation':True,'activityRecreation':True,'mismatchedStateRejected':True,'accountVaultReload':True,'signedAPKDownload':True,'revocation':True,'newAccountsCreated':False},indent=2)+'\n')
 print('PASS: native UI sign-in, real IAM PKCE, callback, cancellation/recreation, APK verification and device revocation.',flush=True)
finally:
 server.shutdown();server.server_close()
 try:fetch('https://gravitas.cool/logout')
 except Exception:pass
