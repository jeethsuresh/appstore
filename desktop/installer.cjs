'use strict';
const fs = require('node:fs/promises');
const path = require('node:path');
const { execFile } = require('node:child_process');
const { promisify } = require('node:util');
const extract = require('./unzip.cjs');
const { artifactFor, validID } = require('./core.cjs');
const execute = promisify(execFile);

// Versioned directories keep running executables untouched and retain the
// previous working version. Registry and shortcuts change only after staging.
async function installPackage({ app, archive, root, current, extractZip = extract }) {
  const artifact = artifactFor(app, current);
  const directory = path.join(root, app.id, String(app.desktopRelease.versionCode));
  await fs.mkdir(path.dirname(directory), { recursive: true, mode: 0o700 });
  const temporary = await fs.mkdtemp(path.join(path.dirname(directory), '.install-'));
  try {
    if (artifact.platform === 'linux') {
      await fs.copyFile(archive, path.join(temporary, artifact.entry));
      await fs.chmod(path.join(temporary, artifact.entry), 0o755);
    } else if (artifact.platform === 'macos' && artifact.file.endsWith('.dmg')) {
      const mount = await fs.mkdtemp(path.join(path.dirname(directory), '.mount-'));
      try {
        await execute('/usr/bin/hdiutil', ['attach', '-nobrowse', '-readonly', '-mountpoint', mount, archive]);
        await fs.cp(path.join(mount, artifact.entry), path.join(temporary, artifact.entry), { recursive: true, verbatimSymlinks: true });
      } finally {
        await execute('/usr/bin/hdiutil', ['detach', mount]).catch(() => {});
        await fs.rmdir(mount).catch(() => {});
      }
    } else if (artifact.platform === 'windows' && artifact.file.endsWith('.exe')) {
      // Unpack Electron NSIS payloads without running their installer or
      // changing an existing app's Windows uninstall registry.
      const sevenZip = require('7zip-bin').path7za.replace('app.asar' + path.sep, 'app.asar.unpacked' + path.sep);
      const payload = await fs.mkdtemp(path.join(path.dirname(directory), '.payload-'));
      try {
        await execute(sevenZip, ['x', '-y', '-o' + payload, archive]);
        const archiveName = artifact.arch === 'arm64' ? 'app-arm64.7z' : 'app-64.7z';
        await execute(sevenZip, ['x', '-y', '-o' + temporary, path.join(payload, '$PLUGINSDIR', archiveName)]);
      } finally { await fs.rm(payload, { recursive: true, force: true }); }
    } else {
      await extractZip(archive, { dir: temporary });
    }
    const entry = path.join(temporary, artifact.entry);
    const stat = await fs.lstat(entry);
    if (stat.isSymbolicLink() || (artifact.platform === 'macos' ? !stat.isDirectory() : !stat.isFile())) throw new Error('The downloaded app has no valid launch entry.');
    // macOS ZIPs contain a complete .app bundle; resolve CFBundleExecutable
    // through plutil when launching, without interpreting manifest shell text.
    await fs.rename(temporary, directory);
    return { version: app.desktopRelease.version, versionCode: app.desktopRelease.versionCode, entry: path.join(directory, artifact.entry), platform: artifact.platform };
  } catch (e) { await fs.rm(temporary, { recursive: true, force: true }); throw e; }
}
async function executable(installed) {
  if (installed.platform !== 'macos') return installed.entry;
  const { stdout } = await execute('/usr/bin/plutil', ['-extract', 'CFBundleExecutable', 'raw', '-o', '-', path.join(installed.entry, 'Contents', 'Info.plist')]);
  const name = stdout.trim();
  if (!name || name !== path.basename(name) || name.includes('\\')) throw new Error('Invalid macOS application executable.');
  return path.join(installed.entry, 'Contents', 'MacOS', name);
}
function quoteDesktop(value) { return '"' + value.replace(/[\\"`$]/g, c => '\\' + c) + '"'; }
async function createShortcut({ app, installed, home, env = process.env }) {
  if (!validID(app.id)) throw new Error('Invalid app identity.');
  const name = app.name.replace(/[\r\n]/g, ' ');
  if (installed.platform === 'linux') {
    const directory = path.join(env.XDG_DATA_HOME || path.join(home, '.local', 'share'), 'applications');
    await fs.mkdir(directory, { recursive: true });
    await fs.writeFile(path.join(directory, `gravitas-${app.id}.desktop`), `[Desktop Entry]\nType=Application\nName=${name}\nExec=${quoteDesktop(installed.entry.replace(/%/g, '%%'))}\nTerminal=false\nCategories=Utility;\n`, { mode: 0o644 });
  } else if (installed.platform === 'macos') {
    const directory = path.join(home, 'Applications');
    await fs.mkdir(directory, { recursive: true });
    const link = path.join(directory, `Gravitas-${app.id}.app`);
    const temporary = link + '.new';
    await fs.rm(temporary, { force: true });
    await fs.symlink(installed.entry, temporary);
    // Only replace a symlink owned by this store; never delete another app.
    try { if (!(await fs.lstat(link)).isSymbolicLink()) throw new Error('An app already exists at ' + link); }
    catch (e) { if (e.code !== 'ENOENT') throw e; }
    await fs.rename(temporary, link);
  } else {
    const directory = path.join(env.APPDATA, 'Microsoft', 'Windows', 'Start Menu', 'Programs', 'Gravitas');
    await fs.mkdir(directory, { recursive: true });
    // Encoded PowerShell uses literal single-quoted paths, never a shell command
    // assembled from remote release fields.
    const literal = s => "'" + s.replace(/'/g, "''") + "'";
    const script = `$s=(New-Object -ComObject WScript.Shell).CreateShortcut(${literal(path.join(directory, app.id + '.lnk'))});$s.TargetPath=${literal(installed.entry)};$s.WorkingDirectory=${literal(path.dirname(installed.entry))};$s.Save()`;
    await execute('powershell.exe', ['-NoProfile', '-NonInteractive', '-EncodedCommand', Buffer.from(script, 'utf16le').toString('base64')]);
  }
}
module.exports = { installPackage, executable, createShortcut, quoteDesktop };
