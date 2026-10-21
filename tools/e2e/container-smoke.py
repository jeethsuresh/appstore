#!/usr/bin/env python3
"""Run the production image read-only as its non-root user, ingest both real releases, and verify protected downloads."""
import argparse,hashlib,json,os,secrets,subprocess,tempfile,time,urllib.request,urllib.error
p=argparse.ArgumentParser();p.add_argument('--engine',default='podman');p.add_argument('--image',default='localhost/private-store:verification');args=p.parse_args()
runId=secrets.token_hex(6);name='private-store-check-'+runId;volume=name+'-data';admin=secrets.token_hex(32)
def run(*command):return subprocess.check_output([args.engine,*command],text=True).strip()
# Environment variables are forwarded by name, so they never appear in command-line arguments.
os.environ['ADMIN_TOKEN']=admin;os.environ['WEBHOOK_SECRET']=secrets.token_hex(32)
run('volume','create',volume)
try:
    run('run','-d','--name',name,'--read-only','--security-opt','no-new-privileges','--cap-drop','ALL','--tmpfs','/tmp:mode=1777,size=64m','-v',volume+':/data','-p','127.0.0.1::8080','-e','ADMIN_TOKEN','-e','WEBHOOK_SECRET','-e','POLL_INTERVAL=0s',args.image)
    address=run('port',name,'8080/tcp').splitlines()[0];base='http://'+address
    def request(path,body=None,method='GET',device=None):
        headers={'Authorization':'Bearer '+(device or admin)}
        if body is not None:headers['Content-Type']='application/json'
        req=urllib.request.Request(base+path,data=json.dumps(body).encode() if body is not None else None,method=method,headers=headers)
        with urllib.request.urlopen(req,timeout=300) as response:
            data=response.read();return json.loads(data) if response.headers.get_content_type()=='application/json' else data
    for _ in range(100):
        try:request('/healthz');break
        except OSError:time.sleep(.1)
    else:raise RuntimeError('Container did not start')
    releases={}
    for app in ['daybreak','spacetime']:
        releases[app]=request('/admin/sync/'+app,method='POST');print(app,'verified',releases[app]['versionName'],flush=True)
    invitation=request('/admin/invites',{'label':'Container smoke test','uses':1,'expiresInHours':1},'POST')['invite']
    device=request('/v1/enroll',{'invite':invitation,'deviceName':'Container test'},'POST')['token']
    for app,release in releases.items():
        data=request('/v1/releases/'+str(release['id'])+'/apk',device=device)
        assert hashlib.sha256(data).hexdigest()==release['sha256'] and len(data)==release['size']
        try:urllib.request.urlopen(base+'/v1/releases/'+str(release['id'])+'/apk');raise AssertionError('Unauthenticated APK access allowed')
        except urllib.error.HTTPError as e:assert e.code==401
    request('/v1/device',method='DELETE',device=device)
    try:request('/v1/apps',device=device);raise AssertionError('Revoked token accepted')
    except urllib.error.HTTPError as e:assert e.code==401
    assert run('exec',name,'id','-u')=='10001'
    run('stop','--time','20',name)
    result={'passed':True,'nonRootUID':10001,'readOnlyRoot':True,'releases':releases,'revocationVerified':True}
    print(json.dumps(result,indent=2))
except Exception:
    print(run('logs',name));raise
finally:
    subprocess.run([args.engine,'rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    subprocess.run([args.engine,'volume','rm',volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
