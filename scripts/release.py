#!/usr/bin/env python3
"""Publish the signed appstore APK and checksums after tests and identity verification."""
import argparse,hashlib,json,os,pathlib,re,shutil,subprocess,tempfile
ROOT=pathlib.Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser();p.add_argument('--publish',action='store_true',help='Publish immediately instead of creating a draft');p.add_argument('--verified-build',action='store_true',help='Reuse an already-tested build; signature/package/version checks always run');args=p.parse_args()
def run(*command,capture=False,env=None):return subprocess.run([str(v) for v in command],cwd=ROOT,env=env,check=True,text=True,capture_output=capture).stdout
if run('git','status','--porcelain',capture=True).strip():raise SystemExit('Commit the release source before publishing.')
if run('git','branch','--show-current',capture=True).strip()!='main':raise SystemExit('Publish from main.')
source=(ROOT/'android/app/build.gradle.kts').read_text();version=re.search(r'versionName = "([^"]+)"',source)[1]
if not re.fullmatch(r'\d+\.\d+\.\d+',version):raise SystemExit('Use a semantic release version.')
tag='v'+version
if not args.verified_build:
    run('go','-C',ROOT/'backend','test','-race','./...');run('go','-C',ROOT/'backend','vet','./...')
    run('python3',ROOT/'scripts/build-android.py')
apk=ROOT/'android/app/build/outputs/apk/release/app-release.apk'
run('python3',ROOT/'scripts/verify-android.py',apk)
dist=ROOT/'dist';dist.mkdir(exist_ok=True);asset=dist/'appstore-android.apk';shutil.copyfile(apk,asset)
(dist/'SHA256SUMS').write_text(hashlib.sha256(asset.read_bytes()).hexdigest()+'  '+asset.name+'\n')
env=os.environ.copy()
if not env.get('GH_TOKEN') and not env.get('GITHUB_TOKEN'):
    credential=subprocess.run(['git','credential','fill'],input='protocol=https\nhost=github.com\n\n',text=True,capture_output=True,env={**env,'GIT_TERMINAL_PROMPT':'0'})
    fields=dict(line.split('=',1) for line in credential.stdout.splitlines() if '=' in line)
    if fields.get('password'):env['GH_TOKEN']=fields['password']
existing=subprocess.run(['gh','release','view',tag,'--repo','jeethsuresh/appstore'],env=env,capture_output=True)
if existing.returncode==0:raise SystemExit(tag+' already exists. Bump versionName and versionCode; published releases are immutable.')
sha=run('git','rev-parse','HEAD',capture=True).strip()
try:tagged=run('git','rev-parse',tag+'^{commit}',capture=True).strip()
except subprocess.CalledProcessError:run('git','tag','-a',tag,'-m','Private Store '+version);tagged=sha
if tagged!=sha:raise SystemExit('Existing tag points to another commit.')
run('git','push','--atomic','origin','main',tag)
command=['gh','release','create',tag,str(asset),str(dist/'SHA256SUMS'),'--repo','jeethsuresh/appstore','--verify-tag','--title','Private Store '+version,'--notes-file',str(ROOT/'docs/RELEASE_NOTES.md')]
if not args.publish:command.append('--draft')
run(*command,env=env)
print('Published release.' if args.publish else 'Created draft release.')
