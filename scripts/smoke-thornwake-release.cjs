'use strict';

const assert = require('node:assert/strict');
const { execFileSync, spawnSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { promises: fs } = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const game = require('./native-game.cjs')(process.argv[3]);
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
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'bazaar-' + game.id + '-'));
  try {
    const app = { id: game.id, name: game.name, desktopRelease: manifest };
    const installed = await installPackage({ app, archive, root: temp, current });
    const appleIdentity = 'anchor apple generic and identifier "' + game.bundle + '" and certificate leaf[subject.OU] = "WV3J3848JR" and certificate leaf[field.1.2.840.113635.100.6.1.13] exists';
    execFileSync('/usr/bin/codesign', ['--verify', '--deep', '--strict', installed.entry], { stdio: 'inherit' });
    execFileSync('/usr/bin/codesign', ['--verify', '-R', '=' + appleIdentity, installed.entry], { stdio: 'inherit' });
    execFileSync('/usr/bin/xcrun', ['stapler', 'validate', installed.entry], { stdio: 'inherit' });
    execFileSync('/usr/sbin/spctl', ['--assess', '--type', 'execute', '--verbose=2', installed.entry], { stdio: 'inherit' });
    const program = await executable(installed);
    function run(args) {
      const process = spawnSync(program, ['--headless', '--', ...args],
        { cwd: temp, encoding: 'utf8', timeout: 60000 });
      const output = (process.stdout || '') + (process.stderr || '');
      assert.equal(process.status, 0, output || process.error?.message);
      assert.doesNotMatch(output, /SCRIPT ERROR|Parse Error|Battle engine|ERROR:/);
      return output;
    }
    const output = run(['--solo', ...(game.enter ? ['--enter'] : []), '--smoke-view', '--until', '30', '--timeout', '30']);
    assert.match(output, new RegExp(game.frame + ' 30'));
    if (game.replay) {
      assert.match(run(['--verify', game.replay]), /EMBER_REPLAY_OK 1800/);
    }
    console.log('Bazaar installed and launched verified ' + game.name + ' on ' + current.arch);
  } finally {
    await fs.rm(temp, { recursive: true, force: true });
  }
}

smoke().catch(error => { console.error(error); process.exitCode = 1; });
