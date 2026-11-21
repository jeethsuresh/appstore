'use strict';
const fs = require('node:fs/promises');
const path = require('node:path');
const { execFile, spawn } = require('node:child_process');
const { promisify } = require('node:util');
const { artifactFor } = require('./core.cjs');
const execute = promisify(execFile);
const ID = 'cool.gravitas.privatestore';
function enabled(env = process.env) { return process.platform === 'linux' && env.FLATPAK_ID === ID; }
function selfTarget(current, env = process.env) { return enabled(env) ? { ...current, format: 'flatpak' } : current; }
function hostCommand(file, args = [], directory) {
  // The host helper uses the host session's environment. Do not forward the
  // sandbox's LD_LIBRARY_PATH, GPU drivers, XDG paths or AppImage variables.
  return { file: '/usr/bin/flatpak-spawn', args: ['--host', ...(directory ? ['--directory=' + directory] : []), file, ...args] };
}
async function host(file, args = [], run = execute) {
  const command = hostCommand(file, args);
  return run(command.file, command.args, { timeout: 20 * 60 * 1000, maxBuffer: 1024 * 1024 });
}
async function dataHome(home, run = execute) {
  try {
    const { stdout } = await host('/usr/bin/printenv', ['XDG_DATA_HOME'], run);
    const directory = stdout.trim();
    if (path.isAbsolute(directory)) return directory;
  } catch (e) { if (e.code !== 1) throw e; }
  return path.join(home, '.local', 'share');
}
async function installationScope({ run = execute, infoFile = '/.flatpak-info' } = {}) {
  const info = await fs.readFile(infoFile, 'utf8');
  const applicationPath = /^app-path=(.+)$/m.exec(info)?.[1];
  if (!applicationPath) throw new Error('The running Flatpak installation could not be identified.');
  for (const scope of ['--user', '--system']) {
    let location;
    try { location = (await host('/usr/bin/flatpak', ['info', scope, '--show-location', ID], run)).stdout.trim(); }
    catch (e) { if (e.killed || e.signal || e.code !== 1) throw e; continue; }
    if (path.join(location, 'files') === applicationPath) return scope;
  }
  throw new Error('This Flatpak installation is unavailable. Install Bazaar for your user with flatpak install --user.');
}
async function installSelf({ app, archive, current, run = execute, scope }) {
  const artifact = artifactFor(app, { ...current, format: 'flatpak' });
  if (artifact.format !== 'flatpak') throw new Error('A Flatpak update is required for this installation.');
  scope ||= await installationScope({ run });
  if (!['--user', '--system'].includes(scope)) throw new Error('Invalid Flatpak installation scope.');
  // archive has already passed the catalog size/SHA-256 check, and is staged
  // in userData, which has the same absolute path inside and outside Flatpak.
  // Flatpak commits the deployment atomically while the old process keeps running.
  const interaction = scope === '--user' ? '--noninteractive' : '--assumeyes';
  await host('/usr/bin/flatpak', ['install', scope, interaction, '--or-update', '--bundle', archive], run);
  return { version: app.desktopRelease.version, versionCode: app.desktopRelease.versionCode, platform: 'linux', format: 'flatpak', entry: ID, scope };
}
async function launchHost(file, args = [], directory, spawnProcess = spawn) {
  const command = hostCommand(file, args, directory);
  await new Promise((resolve, reject) => {
    const child = spawnProcess(command.file, command.args, { detached: true, stdio: 'ignore' });
    let timer;
    const finish = error => {
      clearTimeout(timer);
      child.removeListener('exit', exited);
      if (error) reject(error);
      else { child.unref(); resolve(); }
    };
    const exited = (code, signal) => finish(code === 0 ? null : new Error(`The host could not launch this app (${signal || code}).`));
    child.once('error', finish);
    child.once('exit', exited);
    // A spawned proxy is not yet a launched host app. Surface immediate D-Bus
    // denials or missing executables, without waiting for a game to finish.
    child.once('spawn', () => { timer = setTimeout(() => finish(), 250); });
  });
}
async function brokerLauncher(directory, scope) {
  if (!['--user', '--system'].includes(scope)) throw new Error('Invalid Flatpak installation scope.');
  const file = path.join(directory, 'launch-bazaar');
  await fs.writeFile(file, '#!/bin/sh\nexec /usr/bin/flatpak run ' + scope + ' ' + ID + ' "$@"\n', { mode: 0o755 });
  return { file, args: [] };
}
module.exports = { ID, enabled, selfTarget, hostCommand, host, dataHome, installationScope, installSelf, launchHost, brokerLauncher };
