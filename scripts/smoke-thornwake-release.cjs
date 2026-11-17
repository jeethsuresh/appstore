'use strict';

const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { promises: fs } = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { target } = require('../desktop/core.cjs');
const { executable, installPackage } = require('../desktop/installer.cjs');

async function smoke() {
  const release = path.resolve(process.argv[2]);
  const manifest = JSON.parse(await fs.readFile(path.join(release, 'desktop-release.json'), 'utf8'));
  const current = target();
  assert.equal(current.platform, 'macos');
  const asset = manifest.artifacts.find(a => a.platform === current.platform && a.arch === current.arch);
  assert.ok(asset);
  const archive = path.join(release, asset.file);
  const bytes = await fs.readFile(archive);
  assert.equal(bytes.length, asset.size);
  assert.equal(createHash('sha256').update(bytes).digest('hex'), asset.sha256);
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'bazaar-thornwake-'));
  try {
    const app = { id: 'thornwake', name: 'Thornwake', desktopRelease: manifest };
    const installed = await installPackage({ app, archive, root: temp, current });
    const appleIdentity = 'anchor apple generic and identifier "cool.gravitas.thornwake" and certificate leaf[subject.OU] = "WV3J3848JR" and certificate leaf[field.1.2.840.113635.100.6.1.13] exists';
    execFileSync('/usr/bin/codesign', ['--verify', '--deep', '--strict', installed.entry], { stdio: 'inherit' });
    execFileSync('/usr/bin/codesign', ['--verify', '-R', '=' + appleIdentity, installed.entry], { stdio: 'inherit' });
    execFileSync('/usr/bin/xcrun', ['stapler', 'validate', installed.entry], { stdio: 'inherit' });
    execFileSync('/usr/sbin/spctl', ['--assess', '--type', 'execute', '--verbose=2', installed.entry], { stdio: 'inherit' });
    const output = execFileSync(await executable(installed),
      ['--headless', '--', '--solo', '--smoke-view', '--until', '30', '--timeout', '30'],
      { cwd: temp, encoding: 'utf8', timeout: 40000 });
    assert.match(output, /THORNWAKE_FRAME 30/);
    assert.doesNotMatch(output, /SCRIPT ERROR|Battle engine/);
    console.log('Bazaar installed and launched verified Thornwake on ' + current.arch);
  } finally {
    await fs.rm(temp, { recursive: true, force: true });
  }
}

smoke().catch(error => { console.error(error); process.exitCode = 1; });
