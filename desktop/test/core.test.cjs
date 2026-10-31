const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { createHash } = require('node:crypto');
const { target, artifactFor, updateAvailable, downloadVerified, atomicJSON, readJSON } = require('../core.cjs');
const { installPackage, createShortcut, quoteDesktop } = require('../installer.cjs');
const bytes = Buffer.from('#!/bin/sh\nexit 0\n');
function fixture(code = 2) { return { id: 'example', name: 'Example', desktopRelease: { version: `1.0.${code}`, versionCode: code, artifacts: [{ platform: 'linux', arch: 'x64', file: 'example.AppImage', entry: 'example.AppImage', sha256: createHash('sha256').update(bytes).digest('hex'), size: bytes.length }] } }; }
test('select only the exact operating system and processor; reject paths and ambiguous targets', () => {
  assert.deepEqual(target('darwin', 'arm64'), { platform: 'macos', arch: 'arm64' });
  assert.throws(() => target('linux', 'ia32'));
  assert.throws(() => artifactFor(fixture(), { platform: 'linux', arch: 'arm64' }));
  const app = fixture(); app.desktopRelease.artifacts.push(app.desktopRelease.artifacts[0]); assert.throws(() => artifactFor(app, target('linux', 'x64')));
  for (const field of ['file', 'entry']) { const app = fixture(); app.desktopRelease.artifacts[0][field] = '../outside.AppImage'; assert.throws(() => artifactFor(app, target('linux', 'x64'))); }
});
test('updates apply only to installed apps and never roll back', () => {
  assert.equal(updateAvailable(fixture(), undefined), false);
  assert.equal(updateAvailable(fixture(), { versionCode: 1 }), true);
  assert.equal(updateAvailable(fixture(), { versionCode: 2 }), false);
  assert.equal(updateAvailable(fixture(), { versionCode: 3 }), false);
});
test('stream, verify and install; keep the previous runnable version and shortcut', async t => {
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'store-test-')); t.after(() => fs.rm(temp, { recursive: true, force: true }));
  const app = fixture(1), artifact = app.desktopRelease.artifacts[0], archive = path.join(temp, 'download');
  await downloadVerified(new Response(bytes), archive, artifact);
  const root = path.join(temp, 'apps'), current = target('linux', 'x64');
  const old = await installPackage({ app, archive, root, current });
  const next = await installPackage({ app: fixture(2), archive, root, current });
  assert.equal(await fs.readFile(old.entry, 'utf8'), bytes.toString()); assert.equal(await fs.readFile(next.entry, 'utf8'), bytes.toString());
  if (process.platform !== 'win32') assert.equal((await fs.stat(next.entry)).mode & 0o777, 0o755);
  const dataHome = path.join(temp, 'local');
  await createShortcut({ app, installed: next, home: temp, env: { XDG_DATA_HOME: dataHome } });
  const shortcut = await fs.readFile(path.join(dataHome, 'applications', 'gravitas-example.desktop'), 'utf8');
  assert.ok(shortcut.includes(quoteDesktop(next.entry))); assert.ok(!shortcut.includes(quoteDesktop(old.entry)));
  const statePath = path.join(temp, 'state.json'); await atomicJSON(statePath, { installed: next }); assert.deepEqual(await readJSON(statePath), { installed: next });
});
test('checksum failure and truncated/oversized downloads leave no executable', async t => {
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'store-test-')); t.after(() => fs.rm(temp, { recursive: true, force: true }));
  const artifact = fixture().desktopRelease.artifacts[0], destination = path.join(temp, 'bad');
  for (const data of [Buffer.from('bad'), Buffer.alloc(bytes.length), Buffer.alloc(bytes.length + 1)]) {
    await assert.rejects(downloadVerified(new Response(data), destination, artifact));
    await assert.rejects(fs.access(destination));
  }
});
test('failed extraction never replaces an installed app or leaves a partial directory', async t => {
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'store-test-')); t.after(() => fs.rm(temp, { recursive: true, force: true }));
  const app = fixture(); app.desktopRelease.artifacts[0] = { ...app.desktopRelease.artifacts[0], platform: 'windows', file: 'app.zip', entry: 'App.exe' };
  await assert.rejects(installPackage({ app, archive: 'ignored', root: temp, current: target('win32', 'x64'), extractZip: async () => { throw new Error('Bad archive'); } }));
  assert.deepEqual(await fs.readdir(path.join(temp, 'example')), []);
});
test('desktop shortcut escapes native argument metacharacters', () => { assert.equal(quoteDesktop('/tmp/a"$`\\b'), '"/tmp/a\\"\\$\\`\\\\b"'); });

test('archive extraction preserves internal framework links and rejects escaping or write-through links', { skip: process.platform === 'win32' }, async t => {
  const extract = require('../unzip.cjs');
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'store-zip-')); t.after(() => fs.rm(temp, { recursive: true, force: true }));
  const safe = path.join(temp, 'safe'); await fs.mkdir(safe);
  await extract(path.join(__dirname, 'fixtures', 'safe.zip'), { dir: safe });
  assert.equal(await fs.readFile(path.join(safe, 'App.app', 'Current', 'file'), 'utf8'), 'app');
  for (const fixture of ['escape.zip', 'write-through.zip']) {
    const dir = path.join(temp, fixture); await fs.mkdir(dir);
    await assert.rejects(extract(path.join(__dirname, 'fixtures', fixture), { dir }));
  }
  await assert.rejects(fs.access(path.join(temp, 'outside')));
});
