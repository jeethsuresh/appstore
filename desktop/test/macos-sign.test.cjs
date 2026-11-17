'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync, spawnSync } = require('node:child_process');
const { isMachO, signingOptions } = require('../macos-sign.cjs');

test('every thin and universal Mach-O stays in the timestamped signing set, regardless of filename', t => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'bazaar-code-sign-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const file = path.join(directory, 'locale.pak');
  const optionsForFile = () => ({ hardenedRuntime: true, timestamp: 'http://timestamp.apple.com/ts01' });
  const options = signingOptions({ identity: 'A'.repeat(40), keychain: 'private.keychain', optionsForFile });
  assert.equal(options.identity, 'A'.repeat(40));
  assert.equal(options.keychain, 'private.keychain');
  assert.equal(options.optionsForFile, optionsForFile);
  assert.equal(options.strictVerify, true);
  const resource = options.ignore.at(-1);
  for (const magic of [0xfeedface, 0xcefaedfe, 0xfeedfacf, 0xcffaedfe, 0xcafebabe, 0xbebafeca, 0xcafebabf, 0xbfbafeca]) {
    const data = Buffer.alloc(16); data.writeUInt32BE(magic); fs.writeFileSync(file, data);
    assert.equal(isMachO(file), true, magic.toString(16));
    assert.equal(resource(file), false, 'executable content must never be skipped as data');
  }
  for (const data of [Buffer.alloc(0), Buffer.from([5, 0, 0, 0, 1, 2]), Buffer.from('resource')]) {
    fs.writeFileSync(file, data); assert.equal(resource(file), true);
  }
  assert.equal(resource(directory), false, 'nested bundles still need signatures');
  fs.unlinkSync(file);
  assert.throws(() => resource(file), /ENOENT/, 'unreadable code must fail closed');
});

test('code-aware signing preserves existing exclusions and all per-file security options', () => {
  const ignore = () => false;
  const optionsForFile = () => ({ hardenedRuntime: true, requirements: 'designated requirement', timestamp: 'Apple endpoint', entitlements: 'release.plist' });
  const options = signingOptions({ identity: 'A'.repeat(40), ignore, optionsForFile });
  assert.equal(options.ignore[0], ignore);
  assert.deepEqual(options.optionsForFile('/app'), optionsForFile('/app'));
});

test('release signing cannot fall back to ad-hoc or omit its keychain', () => {
  assert.throws(() => signingOptions({ identity: '-' }, true), /Developer ID/);
  assert.throws(() => signingOptions({ identity: 'A'.repeat(40) }, true), /keychain/);
  assert.equal(signingOptions({ identity: 'A'.repeat(40), keychain: 'private.keychain' }, true).identity, 'A'.repeat(40));
});

test('unsigned resource data remains sealed and tampering fails strict bundle verification', { skip: process.platform !== 'darwin' }, t => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'bazaar-resource-seal-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const app = path.join(directory, 'Fixture.app');
  const contents = path.join(app, 'Contents');
  fs.mkdirSync(path.join(contents, 'MacOS'), { recursive: true });
  fs.mkdirSync(path.join(contents, 'Resources'), { recursive: true });
  fs.writeFileSync(path.join(contents, 'Info.plist'), '<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>cool.gravitas.signing-test</string><key>CFBundleExecutable</key><string>Fixture</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleVersion</key><string>1</string></dict></plist>');
  const executable = path.join(contents, 'MacOS', 'Fixture');
  fs.copyFileSync('/usr/bin/true', executable);
  const resource = path.join(contents, 'Resources', 'locale.pak');
  fs.writeFileSync(resource, Buffer.from([5, 0, 0, 0, 1, 2, 3]));
  const filter = signingOptions({}).ignore.at(-1);
  assert.equal(filter(executable), false);
  assert.equal(filter(resource), true);
  // Test-only ad-hoc signatures need no private key. Production still requires
  // Developer ID, secure timestamps and Apple's notarization in native smoke.
  execFileSync('/usr/bin/codesign', ['--force', '--sign', '-', executable]);
  execFileSync('/usr/bin/codesign', ['--force', '--sign', '-', app]);
  execFileSync('/usr/bin/codesign', ['--verify', '--deep', '--strict', app]);
  fs.appendFileSync(resource, 'tampered');
  assert.notEqual(spawnSync('/usr/bin/codesign', ['--verify', '--deep', '--strict', app]).status, 0);
});
