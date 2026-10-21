#!/usr/bin/env python3
"""Create the store's permanent key once; keep it in the ignored private .signing folder."""
import json,os,pathlib,secrets,subprocess,hashlib
root=pathlib.Path(__file__).resolve().parents[1]
folder=root/'.signing'
folder.mkdir(mode=0o700,exist_ok=True)
os.chmod(folder,0o700)
if any(folder.iterdir()):raise SystemExit('Signing folder is not empty; preserve the existing key.')
config={'storeFile':str(folder/'private-store-release.jks'),'storePassword':secrets.token_urlsafe(32),'keyAlias':'private-store-release','keyPassword':secrets.token_urlsafe(32)}
env={**os.environ,'STORE_STORE_PASSWORD':config['storePassword'],'STORE_KEY_PASSWORD':config['keyPassword']}
java=os.environ.get('JAVA_HOME','')
keytool=str(pathlib.Path(java)/'bin/keytool') if java else 'keytool'
subprocess.run([keytool,'-genkeypair','-keystore',config['storeFile'],'-storetype','JKS','-alias',config['keyAlias'],'-storepass:env','STORE_STORE_PASSWORD','-keypass:env','STORE_KEY_PASSWORD','-keyalg','RSA','-keysize','4096','-validity','10000','-dname','CN=Private Store Android Release, O=Jeeth Suresh'],env=env,check=True,capture_output=True)
os.chmod(config['storeFile'],0o600)
path=folder/'signing.json'
fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
with os.fdopen(fd,'w') as f:json.dump(config,f,indent=2);f.write('\n')
certificate=subprocess.check_output([keytool,'-exportcert','-keystore',config['storeFile'],'-alias',config['keyAlias'],'-storepass:env','STORE_STORE_PASSWORD'],env=env)
(root/'android/release-certificate.sha256').write_text(hashlib.sha256(certificate).hexdigest()+'\n')
print('Created permanent store signing key in the ignored .signing directory. Back up that directory securely.')
