#!/usr/bin/env python3
"""Verify the real Compose/Caddy deployment locally, on private random ports, then remove only its own test resources."""
import argparse,hashlib,json,os,pathlib,secrets,ssl,subprocess,tempfile,time,urllib.request,urllib.error
ROOT=pathlib.Path(__file__).resolve().parents[2]
p=argparse.ArgumentParser();p.add_argument('--compose',default='docker');p.add_argument('--podman',action='store_true');p.add_argument('--image',default='localhost/private-store:verification');args=p.parse_args()
env={**os.environ,'ADMIN_TOKEN':secrets.token_hex(32),'WEBHOOK_SECRET':secrets.token_hex(32),'STORE_DOMAIN':'localhost'}
if args.podman:
    sock=subprocess.check_output(['podman','machine','inspect','--format','{{.ConnectionInfo.PodmanSocket.Path}}'],text=True).strip();env['DOCKER_HOST']='unix://'+sock
cli=[args.compose] if pathlib.Path(args.compose).name=='docker-compose' else [args.compose,'compose']
project='private-store-smoke-'+secrets.token_hex(5)
with tempfile.TemporaryDirectory(prefix='private-store-compose-') as tmp:
    work=pathlib.Path(tmp);override=work/'override.json'
    # JSON is a valid Compose/YAML document; !override ports is supplied as YAML below.
    override.write_text('services:\n  store:\n    image: '+args.image+'\n    pull_policy: never\n    environment:\n      POLL_INTERVAL: 0s\n  caddy:\n    ports: !override\n      - "127.0.0.1::443"\n')
    base=[*cli,'--project-name',project,'--env-file',str(ROOT/'.env.example'),'-f',str(ROOT/'compose.yaml'),'-f',str(override)]
    def compose(*command,capture=True):return subprocess.run([*base,*command],check=True,text=True,env=env,capture_output=capture).stdout
    try:
        compose('config','--quiet')
        print('Starting local Compose stack on a random loopback HTTPS port…',flush=True)
        compose('up','-d','--no-build','--wait','--wait-timeout','120')
        port=compose('port','caddy','443').strip().split(':')[-1]
        caddy=compose('ps','-q','caddy').strip()
        engine=['podman'] if args.podman else ['docker']
        for _ in range(50):
            result=subprocess.run([*engine,'cp',caddy+':/data/caddy/pki/authorities/local/root.crt',str(work/'ca.pem')],capture_output=True)
            if result.returncode==0:break
            time.sleep(.2)
        else:raise RuntimeError('Caddy did not create its local HTTPS certificate')
        context=ssl.create_default_context(cafile=str(work/'ca.pem'));url='https://localhost:'+port
        def api(path,body=None,method='GET',token=None):
            headers={'Authorization':'Bearer '+(token or env['ADMIN_TOKEN'])}
            if body is not None:headers['Content-Type']='application/json'
            request=urllib.request.Request(url+path,data=json.dumps(body).encode() if body is not None else None,method=method,headers=headers)
            with urllib.request.urlopen(request,context=context,timeout=300) as response:
                data=response.read();return json.loads(data) if response.headers.get_content_type()=='application/json' else data
        assert api('/healthz')['status']=='ok'
        try:urllib.request.urlopen(url+'/v1/apps',context=context);raise AssertionError('Anonymous catalog access')
        except urllib.error.HTTPError as e:assert e.code==401
        releases={}
        for app in ['daybreak','spacetime']:
            releases[app]=api('/admin/sync/'+app,method='POST');print(app,'ingested behind HTTPS proxy',flush=True)
        invite=api('/admin/invites',{'label':'Compose HTTPS test','uses':1,'expiresInHours':1},'POST')['invite']
        token=api('/v1/enroll',{'invite':invite,'deviceName':'Compose smoke'},'POST')['token']
        assert len(api('/v1/apps',token=token)['apps'])==2
        for app,release in releases.items():
            data=api('/v1/releases/'+str(release['id'])+'/apk',token=token)
            assert len(data)==release['size'] and hashlib.sha256(data).hexdigest()==release['sha256']
        api('/v1/device',method='DELETE',token=token)
        try:api('/v1/apps',token=token);raise AssertionError('Revoked device accepted')
        except urllib.error.HTTPError as e:assert e.code==401
        print('PASS: Compose health checks, Caddy HTTPS, real release ingestion, private downloads and revocation.',flush=True)
    except Exception:
        print(compose('logs','--no-color'));raise
    finally:compose('down','--volumes','--remove-orphans')
