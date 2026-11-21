'use strict';
// Run only on a Linux x86-64 host with Flatpak and both test bundles built.
// A separate bus, XDG directories and test-only filesystem grant keep the
// user's Store settings and native sign-in broker out of this smoke test.
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { execFile, spawn } = require('node:child_process');
const { promisify } = require('node:util');
const { createHash } = require('node:crypto');
const assert = require('node:assert/strict');
const execute = promisify(execFile);
const { _electron } = require('@playwright/test');
const packageInfo = require('../package.json');
const { ID } = require('../flatpak.cjs');
async function main() {
  if (process.platform !== 'linux' || process.arch !== 'x64') throw new Error('Flatpak smoke requires Linux x86-64.');
  if (!process.env.BAZAAR_FLATPAK_SMOKE_ROOT) {
    const root = await fs.mkdtemp(path.join(os.tmpdir(), 'bazaar-flatpak-smoke-'));
    try {
      const env = { ...process.env, BAZAAR_FLATPAK_SMOKE_ROOT: root,
        FLATPAK_USER_DIR: process.env.FLATPAK_USER_DIR || path.join(process.env.XDG_DATA_HOME || path.join(os.homedir(), '.local', 'share'), 'flatpak'),
        XDG_CONFIG_HOME: path.join(root, 'config'), XDG_DATA_HOME: path.join(root, 'data'), XDG_CACHE_HOME: path.join(root, 'cache') };
      // Flatpak's activated background services can inherit output descriptors.
      // Wait for the session's exit, not for every inherited pipe to close.
      await new Promise((resolve, reject) => {
        const child = spawn('dbus-run-session', ['--', process.execPath, __filename], { env, stdio: 'inherit' });
        const timer = setTimeout(() => { child.kill(); reject(new Error('Flatpak smoke timed out.')); }, 300000);
        child.once('error', error => { clearTimeout(timer); reject(error); });
        child.once('exit', (code, signal) => { clearTimeout(timer); code === 0 ? resolve() : reject(new Error(`Flatpak smoke failed (${signal || code}).`)); });
      });
    }
    finally { await fs.rm(root, { recursive: true, force: true, maxRetries: 20, retryDelay: 100 }); }
    return;
  }
  const root = process.env.BAZAAR_FLATPAK_SMOKE_ROOT;
  const dist = path.resolve(__dirname, '../dist');
  const nextVersion = packageInfo.version.split('.').map((n, i) => i === 2 ? Number(n) + 1 : n).join('.');
  const first = path.join(dist, `PrivateStore-${packageInfo.version}-linux-x86_64.flatpak`);
  const next = path.join(dist, 'flatpak-smoke', `PrivateStore-${nextVersion}-linux-x86_64.flatpak`);
  await fs.access(first); await fs.access(next);
  let exists = false;
  try { await execute('flatpak', ['info', '--user', ID]); exists = true; }
  catch (e) { if (e.code !== 1) throw e; }
  if (exists) throw new Error('Refusing to replace an existing user installation of Bazaar.');
  const launcher = path.join(root, 'launch');
  await fs.writeFile(launcher, `#!/bin/sh\nexec /usr/bin/flatpak run --user --socket=x11 --filesystem="${root}" --filesystem="${path.resolve(__dirname, '..')}:ro" --env=XDG_CONFIG_HOME="${root}/config" --env=XDG_DATA_HOME="${root}/data" ${ID} "$@"\n`, { mode: 0o755 });
  let electron;
  const open = async () => {
    electron = await _electron.launch({ executablePath: launcher, args: ['--ozone-platform=x11', '--password-store=basic', '--user-data-dir=' + path.join(root, 'profile')], chromiumSandbox: true, timeout: 90000 });
    const page = await electron.firstWindow({ timeout: 30000 });
    await page.waitForSelector('#content');
    assert.equal(await page.title(), 'Bazaar');
    return page;
  };
  try {
    console.log('Installing the first Flatpak and opening its packaged UI.');
    await execute('flatpak', ['install', '--user', '--noninteractive', '--bundle', first], { timeout: 120000 });
    let page = await open();
    assert.equal(await electron.evaluate(({ app }) => app.getPath('userData')), path.join(root, 'profile'));
    assert.equal(await electron.evaluate(({ app }) => app.getVersion()), packageInfo.version);
    await page.evaluate(() => window.store.action('settings', false));
    console.log('Installing and updating a managed game, then applying the verified Flatpak update.');
    const state = await electron.evaluate(async ({ app }, { root, nextVersion, code, digest, size }) => {
      const fs = process.getBuiltinModule('node:fs/promises');
      const path = process.getBuiltinModule('node:path');
      const load = process.getBuiltinModule('node:module').createRequire(path.join(app.getAppPath(), 'main.cjs'));
      const flatpak = load('./flatpak.cjs'), installer = load('./installer.cjs');
      const current = { platform: 'linux', arch: 'x64' };
      const apps = path.join(root, 'managed-apps'), marker = path.join(root, 'game-marker');
      let installed;
      for (const versionCode of [1, 2]) {
        const bytes = '#!/bin/sh\nprintf "%s\\n" "game-' + versionCode + '" "${FLATPAK_ID-unset}" "${LD_LIBRARY_PATH-unset}" > "$1"\n';
        const archive = path.join(root, 'game.AppImage');
        await fs.writeFile(archive, bytes);
        const game = { id: 'smoke-game', desktopRelease: { version: `1.0.${versionCode}`, versionCode, artifacts: [{ ...current, file: 'Game.AppImage', entry: 'Game.AppImage', sha256: process.getBuiltinModule('node:crypto').createHash('sha256').update(bytes).digest('hex'), size: Buffer.byteLength(bytes) }] } };
        installed = await installer.installPackage({ app: game, archive, root: apps, current });
      }
      await flatpak.launchHost(installed.entry, [marker]);
      const metadata = { id: 'appstore', desktopRelease: { version: nextVersion, versionCode: code, artifacts: [{ ...current, format: 'flatpak', file: 'Bazaar.flatpak', entry: flatpak.ID, sha256: digest, size }] } };
      // Exercise the same verified download boundary used by the UI before
      // handing the real bundle to Flatpak's atomic update transaction.
      const staged = path.join(root, 'verified-update.flatpak');
      await load('./core.cjs').downloadVerified(new Response(await fs.readFile(path.join(root, 'update.flatpak'))), staged, metadata.desktopRelease.artifacts[0]);
      const record = await flatpak.installSelf({ app: metadata, archive: staged, current });
      return { record, version: app.getVersion(), userData: app.getPath('userData'), marker, hostDataHome: await flatpak.dataHome(process.env.HOME) };
    }, await (async () => {
      const bytes = await fs.readFile(next);
      await fs.writeFile(path.join(root, 'update.flatpak'), bytes);
      return { root, nextVersion, code: packageInfo.versionCode + 1, digest: createHash('sha256').update(bytes).digest('hex'), size: bytes.length };
    })());
    assert.equal(state.record.format, 'flatpak');
    assert.equal(state.record.scope, '--user');
    assert.equal(state.version, packageInfo.version, 'the running deployment stays intact until restart');
    assert.equal(state.hostDataHome, path.join(root, 'data'));
    let marker;
    for (let i = 0; i < 100; i++) {
      try { marker = await fs.readFile(state.marker, 'utf8'); break; }
      catch (e) { if (e.code !== 'ENOENT') throw e; await new Promise(r => setTimeout(r, 100)); }
    }
    assert.ok(marker?.startsWith('game-2\nunset\n'), 'updated managed game must run on the host');
    assert.ok(!marker.includes('/app/') && !marker.includes('/usr/lib/extensions/'), 'sandbox library paths must not leak into games');
    const broker = JSON.parse(await fs.readFile(path.join(root, 'data/Gravitas/Private Store/broker-v1.json'), 'utf8'));
    assert.ok(broker.launch.file.startsWith(state.userData + path.sep));
    assert.match(await fs.readFile(broker.launch.file, 'utf8'), /flatpak run --user cool\.gravitas\.privatestore/);
    await electron.close(); electron = null;
    console.log('Relaunching the updated deployment and checking retained settings.');
    page = await open();
    assert.equal(await electron.evaluate(({ app }) => app.getVersion()), nextVersion);
    assert.equal((await page.evaluate(() => window.store.action('state'))).settings.autoUpdate, false);
    assert.equal((await page.evaluate(() => window.store.action('state'))).installed.appstore.versionCode, packageInfo.versionCode + 1);
    assert.equal(await electron.evaluate(({ app }) => app.getPath('userData')), state.userData);
    assert.ok((await fs.readFile(state.marker, 'utf8')).startsWith('game-2\n'));
    console.log('Verified Flatpak sandbox UI, host game install/update/launch, atomic self-update, relaunch, broker discovery and preserved settings.');
  } finally {
    if (electron) await electron.close();
    await execute('flatpak', ['uninstall', '--user', '--noninteractive', ID]).catch(e => console.error('Smoke installation cleanup failed:', e.message));
  }
}
main().catch(e => { console.error(e); process.exitCode = 1; });
