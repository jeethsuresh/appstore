#!/usr/bin/env python3
"""Minimal administrative CLI. Credentials are read from environment variables."""
import argparse, json, os, sys, urllib.error, urllib.parse, urllib.request
p=argparse.ArgumentParser()
sub=p.add_subparsers(dest='command',required=True)
a=sub.add_parser('invite');a.add_argument('--label',default='Phone');a.add_argument('--uses',type=int,default=1);a.add_argument('--hours',type=int,default=24)
sub.add_parser('devices')
a=sub.add_parser('revoke');a.add_argument('device_id')
a=sub.add_parser('sync');a.add_argument('app', choices=['daybreak','spacetime','appstore','launcher','river','atlas','haven']);a.add_argument('--tag')
args=p.parse_args()
base=os.environ.get('STORE_URL','https://store.example.com').rstrip('/')
if not base.startswith('https://') and not base.startswith('http://127.0.0.1:'):
    p.error('Use HTTPS or localhost for STORE_URL')
token=os.environ.get('ADMIN_TOKEN')
if not token:p.error('Set ADMIN_TOKEN in the environment')
method,path,body='GET','/admin/devices',None
if args.command=='invite':method,path,body='POST','/admin/invites',{'label':args.label,'uses':args.uses,'expiresInHours':args.hours}
if args.command=='revoke':method,path='DELETE','/admin/devices/'+urllib.parse.quote(args.device_id,safe='')
if args.command=='sync':method,path='POST','/admin/sync/'+args.app+('?tag='+urllib.parse.quote(args.tag,safe='') if args.tag else '')
request=urllib.request.Request(base+path,data=json.dumps(body).encode() if body else None,method=method,headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
# Do not follow redirects carrying admin credentials.
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args,**kwargs):return None
try:
    with urllib.request.build_opener(NoRedirect).open(request,timeout=600) as response:
        data=response.read();print(json.dumps(json.loads(data),indent=2) if data else 'Done.')
except urllib.error.HTTPError as e:
    print(f'HTTP {e.code}: {e.read().decode()}',file=sys.stderr);sys.exit(1)
