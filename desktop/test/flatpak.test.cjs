const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { EventEmitter } = require('node:events');
const { artifactFor, releasePath } = require('../core.cjs');
const flatpak = require('../flatpak.cjs');
const current = { platform: 'linux', arch: 'x64' };
function fixture() {
  const artifact = { ...current, file: 'Bazaar.flatpak', entry: flatpak.ID, format: 'flatpak', sha256: 'a'.repeat(64), size: 123 };
  return { id: 'appstore', desktopRelease: { version: '1.0.0', versionCode: 2, artifacts: [artifact, { ...artifact, format: undefined, file: 'Bazaar.AppImage', entry: 'Bazaar.AppImage' }] } };
}
test('Flatpak updates stay on their own package format and portable clients retain AppImage', () => {
  const app = fixture(), packaged = { ...current, format: 'flatpak' };
  assert.equal(artifactFor(app, current).file, 'Bazaar.AppImage');
  assert.equal(artifactFor(app, packaged).file, 'Bazaar.flatpak');
  assert.match(releasePath(app, packaged), /\?format=flatpak$/);
  assert.doesNotMatch(releasePath(app, current), /\?/);
  app.desktopRelease.artifacts.shift();
  assert.throws(() => artifactFor(app, packaged), /No compatible/);
});
test('unsupported Flatpak identities, architectures and disguised packages fail closed', () => {
  for (const mutate of [a => a.entry = 'org.other.App', a => a.file = 'Bazaar.AppImage', a => a.arch = 'arm64']) {
    const app = fixture(); mutate(app.desktopRelease.artifacts[0]);
    assert.throws(() => artifactFor(app, { ...current, format: 'flatpak' }));
  }
});
test('self-update uses Flatpak atomic installation with argv, and propagates transaction failures', async () => {
  const calls = [], archive = '/home/user/.var/app/Bazaar/update with spaces;literal.flatpak';
  const run = async (...args) => { calls.push(args); return { stdout: '' }; };
  const record = await flatpak.installSelf({ app: fixture(), archive, current, scope: '--user', run });
  assert.equal(record.entry, flatpak.ID);
  assert.equal(record.format, 'flatpak');
  assert.equal(record.versionCode, 2);
  assert.deepEqual(calls[0].slice(0, 2), ['/usr/bin/flatpak-spawn', ['--host', '/usr/bin/flatpak', 'install', '--user', '--noninteractive', '--or-update', '--bundle', archive]]);
  assert.equal(calls[0][2].shell, undefined);
  await assert.rejects(flatpak.installSelf({ app: fixture(), archive, current, scope: '--system', run: async () => { throw new Error('transaction failed'); } }), /transaction failed/);
  await assert.rejects(flatpak.installSelf({ app: fixture(), archive, current, scope: '--bad', run }), /scope/);
  assert.equal(calls.length, 1);
});
test('identify the running installation, including when another scope shadows the same app', async t => {
  if (process.platform !== 'linux') { t.skip('Flatpak deployment paths are Linux-specific.'); return; }
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'flatpak-scope-'));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  const infoFile = path.join(directory, 'info');
  for (const expected of ['--user', '--system']) {
    await fs.writeFile(infoFile, '[Instance]\napp-path=/' + expected.slice(2) + '/deployment/files\n');
    const run = async (_file, args) => ({ stdout: '/' + args[3].slice(2) + '/deployment\n' });
    assert.equal(await flatpak.installationScope({ run, infoFile }), expected);
  }
  await assert.rejects(flatpak.installationScope({ infoFile, run: async () => { throw new Error('host permission denied'); } }), /permission denied/);
});
test('host integration uses its XDG directory, with the standard fallback when unset', async () => {
  const home = os.homedir();
  const custom = path.join(home, 'custom-data');
  assert.equal(await flatpak.dataHome(home, async () => ({ stdout: custom + '\n' })), custom);
  assert.equal(await flatpak.dataHome(home, async () => { throw Object.assign(new Error('unset'), { code: 1 }); }), path.join(home, '.local', 'share'));
  await assert.rejects(flatpak.dataHome(home, async () => { throw new Error('host unavailable'); }), /host unavailable/);
});
test('games launch outside the sandbox without forwarding its library or graphics environment', async () => {
  let call;
  await flatpak.launchHost('/games/Game with spaces.AppImage', ['--literal=$value'], '/games', (...args) => {
    call = args;
    const child = new EventEmitter(); child.unref = () => {};
    process.nextTick(() => child.emit('spawn'));
    return child;
  });
  assert.deepEqual(call.slice(0, 2), ['/usr/bin/flatpak-spawn', ['--host', '--directory=/games', '/games/Game with spaces.AppImage', '--literal=$value']]);
  assert.equal(call[2].env, undefined);
  assert.equal(call[2].shell, undefined);
});
test('a rejected host launch is reported instead of appearing successful', async () => {
  await assert.rejects(flatpak.launchHost('/missing/game', [], undefined, () => {
    const child = new EventEmitter(); child.unref = () => {};
    process.nextTick(() => { child.emit('spawn'); child.emit('exit', 1, null); });
    return child;
  }), /could not launch/);
});
test('native apps can cold-start the Flatpak with the existing broker SDK', async t => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'flatpak-launcher-'));
  t.after(() => fs.rm(directory, { recursive: true, force: true }));
  const launch = await flatpak.brokerLauncher(directory, '--user');
  assert.deepEqual(launch.args, []);
  assert.equal(await fs.readFile(launch.file, 'utf8'), '#!/bin/sh\nexec /usr/bin/flatpak run --user cool.gravitas.privatestore "$@"\n');
  if (process.platform !== 'win32') assert.equal((await fs.stat(launch.file)).mode & 0o777, 0o755);
});
