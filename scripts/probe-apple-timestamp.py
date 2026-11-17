import concurrent.futures
import hashlib
import ipaddress
import json
import os
import socket
import urllib.parse
import urllib.request

host='timestamp.apple.com'
addresses={a[4][0] for a in socket.getaddrinfo(host,80,socket.AF_INET,socket.SOCK_STREAM)}
print('System IPv4:',sorted(addresses),flush=True)
print('Proxy environment present:', {key:bool(os.environ.get(key)) for key in ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY']},flush=True)
for subnet in ['0.0.0.0/0','17.32.213.0/24','17.179.249.0/24']:
 try:
  query=urllib.parse.urlencode({'name':host,'type':'A','edns_client_subnet':subnet})
  with urllib.request.urlopen('https://dns.google/resolve?'+query,timeout=15) as r:data=json.load(r)
  answer=[a['data'] for a in data.get('Answer',[]) if a.get('type')==1]
  print('Public DNS:',subnet,answer,flush=True)
  addresses.update(answer)
 except Exception as error:print('Public DNS error:',str(error),flush=True)
addresses={a for a in addresses if ipaddress.IPv4Address(a) in ipaddress.IPv4Network('17.0.0.0/8')}
request=bytes.fromhex('30390201013031300d060960864801650304020105000420')+hashlib.sha256(b'Bazaar release code-signing timestamp connectivity probe').digest()+bytes.fromhex('0101ff')
def probe(url):
 try:
  req=urllib.request.Request(url,data=request,headers={'Content-Type':'application/timestamp-query'})
  with urllib.request.urlopen(req,timeout=15) as response:
   body=response.read(65536)
   return {'url':url,'status':response.status,'type':response.headers.get('Content-Type'),'bytes':len(body),'prefix':body[:18].hex()}
 except Exception as error:return {'url':url,'error':str(error)}
urls=['http://'+host+'/ts01']+['http://'+a+'/ts01' for a in sorted(addresses)]
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
 for result in pool.map(probe,urls):print(json.dumps(result),flush=True)
