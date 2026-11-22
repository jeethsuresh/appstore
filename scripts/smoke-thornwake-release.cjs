'use strict';

const assert = require('node:assert/strict');
const { execFileSync, spawnSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { promises: fs } = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const nativeGame = require('./native-game.cjs');
const { target } = require('../desktop/core.cjs');
const { executable, installPackage } = require('../desktop/installer.cjs');

function verifyFreight(report) {
  assert.equal(report.map, 'freight');
  assert.equal(report.mode, 'deathmatch');
  assert.equal(report.server, true);
  assert.equal(report.actors, 2);
  assert.equal(report.peers, 0);
  assert.ok(Number.isFinite(report.elapsed) && report.elapsed >= 2);
  assert.ok(Object.hasOwn(report.positions, '1'));
  assert.equal(Object.keys(report.positions).length, 2);
  for (const coordinates of Object.values(report.positions)) {
    assert.ok(Array.isArray(coordinates) && coordinates.length === 3 && coordinates.every(Number.isFinite));
  }
}

async function runInstalledGame(game, program, directory) {
  function run(args) {
    const child = spawnSync(program, ['--headless', '--', ...args], { cwd: directory, env: { ...process.env, XDG_CONFIG_HOME: path.join(directory, 'config'), XDG_DATA_HOME: path.join(directory, 'data'), XDG_CACHE_HOME: path.join(directory, 'cache') }, encoding: 'utf8', timeout: 60000 });
    const output = (child.stdout || '') + (child.stderr || '');
    assert.equal(child.status, 0, output || child.error?.message);
    assert.doesNotMatch(output, /SCRIPT ERROR|Parse Error|Battle engine|ERROR:/);
    return output;
  }
  if (game.id === 'railyard-fps') {
    const reportFile = path.join(directory, 'freight.json');
    const output = run(['--map=freight', '--seconds=2', '--bots=1', '--report=' + reportFile]);
    const summary = output.match(/^RAILYARD_REPORT (.+)$/m);
    assert.ok(summary, 'Freight match did not report completion');
    const report = JSON.parse(await fs.readFile(reportFile, 'utf8'));
    assert.deepEqual(report, JSON.parse(summary[1]));
    verifyFreight(report);
  } else {
    const output = run(['--solo', ...(game.enter ? ['--enter'] : []), '--smoke-view', '--record', path.join(directory, 'installer-smoke.replay'), '--until', '30', '--timeout', '30']);
    assert.match(output, new RegExp(game.frame + ' 30'));
    if (game.replay) assert.match(run(['--verify', game.replay]), /EMBER_REPLAY_OK 1800/);
  }
}

async function smoke() {
  const game = nativeGame(process.argv[3]);
  const release = path.resolve(process.argv[2]);
  const manifest = JSON.parse(await fs.readFile(path.join(release, 'desktop-release.json'), 'utf8'));
  const current = target();
  const unsigned = process.argv.includes('--unsigned');
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
    assert.equal(installed.entry, path.join(temp, game.id, String(manifest.versionCode), asset.entry));
    if (current.platform === 'macos' && !unsigned) {
      const appleIdentity = 'anchor apple generic and identifier "' + game.bundle + '" and certificate leaf[subject.OU] = "WV3J3848JR" and certificate leaf[field.1.2.840.113635.100.6.1.13] exists';
      execFileSync('/usr/bin/codesign', ['--verify', '--deep', '--strict', installed.entry], { stdio: 'inherit' });
      execFileSync('/usr/bin/codesign', ['--verify', '-R', '=' + appleIdentity, installed.entry], { stdio: 'inherit' });
      execFileSync('/usr/bin/xcrun', ['stapler', 'validate', installed.entry], { stdio: 'inherit' });
      execFileSync('/usr/sbin/spctl', ['--assess', '--type', 'execute', '--verbose=2', installed.entry], { stdio: 'inherit' });
    }
    const program = await executable(installed);
    if (current.platform === 'macos') assert.equal(program, path.join(installed.entry, 'Contents', 'MacOS', game.name));
    else assert.equal(program, installed.entry);
    await runInstalledGame(game, program, temp);
    console.log('Bazaar installed and launched verified ' + game.name + ' on ' + current.arch);
  } finally {
    await fs.rm(temp, { recursive: true, force: true });
  }
}

module.exports = { runInstalledGame, verifyFreight };
if (require.main === module) {
  smoke().catch(error => { console.error(error); process.exitCode = 1; });
}
