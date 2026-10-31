'use strict';
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { createHash } = require('node:crypto');
const { _electron } = require('@playwright/test');
const { target, downloadVerified } = require('../core.cjs');
const { installPackage, executable } = require('../installer.cjs');
const packageInfo = require('../package.json');

async function smoke() {
  const current = target();
  if (process.argv[2] !== current.arch) { console.log('Cross-architecture package built; launch check requires a matching CPU.'); return; }
  const temporary = await fs.mkdtemp(path.join(os.tmpdir(), 'store-native-'));
  try {
    const prefix = { macos: 'mac', windows: 'win', linux: 'linux' }[current.platform];
    const pattern = new RegExp(`^PrivateStore-${packageInfo.version.replaceAll('.', '\\.')}\-${prefix}\-${current.arch === 'x64' ? '(x64|x86_64)' : '(arm64|aarch64)'}\\.(zip|AppImage)$`);
    const files = (await fs.readdir('dist')).filter(name => pattern.test(name));
    if (files.length !== 1) throw new Error('Missing native package');
    const file = path.resolve('dist', files[0]), bytes = await fs.readFile(file);
    const artifact = { ...current, file: files[0], entry: current.platform === 'macos' ? 'Private Store.app' : current.platform === 'windows' ? 'Private Store.exe' : files[0], sha256: createHash('sha256').update(bytes).digest('hex'), size: bytes.length };
    const release = { id: 'appstore', name: 'Private Store', desktopRelease: { version: packageInfo.version, versionCode: packageInfo.versionCode, artifacts: [artifact] } };
    const installed = await installPackage({ app: release, archive: file, root: path.join(temporary, 'apps'), current });
    const electron = await _electron.launch({ executablePath: await executable(installed), args: ['--no-sandbox'], env: { ...process.env, APPIMAGE_EXTRACT_AND_RUN: '1', XDG_CONFIG_HOME: temporary, XDG_DATA_HOME: path.join(temporary, 'local') }, timeout: 60000 });
    try { const page = await electron.firstWindow(); await page.waitForSelector('#content'); if (await page.title() !== 'Private Store') throw new Error('Packaged UI failed to load'); }
    finally { await electron.close(); }
    console.log('Verified native package extraction and launch for ' + current.platform + '/' + current.arch);
    // Exercise both existing native installer conventions on matching runners.
    if ((current.platform === 'windows' && current.arch === 'x64') || (current.platform === 'macos' && current.arch === 'arm64')) {
      const name = current.platform === 'macos' ? 'RSS-Reader-0.1.4-mac-arm64.dmg' : 'RSS-Reader-0.1.4-win-x64.exe';
      const response = await fetch('https://api.github.com/repos/jeethsuresh/rss/releases/tags/v0.1.4');
      if (!response.ok) throw new Error('Could not load native compatibility release metadata');
      const asset = (await response.json()).assets.find(asset => asset.name === name);
      if (!asset?.digest?.startsWith('sha256:')) throw new Error('Missing native fixture checksum');
      const metadata = { ...current, file: name, entry: current.platform === 'macos' ? 'RSS Reader.app' : 'RSS Reader.exe', sha256: asset.digest.slice(7), size: asset.size };
      const archive = path.join(temporary, name);
      await downloadVerified(await fetch(asset.browser_download_url), archive, metadata);
      const rss = { id: 'rss', name: 'RSS Reader', desktopRelease: { version: '0.1.4', versionCode: 1004, artifacts: [metadata] } };
      const result = await installPackage({ app: rss, archive, root: path.join(temporary, 'apps'), current });
      await fs.access(await executable(result));
      console.log('Verified existing RSS Reader native installer compatibility');
    }
  } finally { await fs.rm(temporary, { recursive: true, force: true }); }
}
smoke().catch(error => { console.error(error); process.exit(1); });
