'use strict';
const { createHash } = require('node:crypto');
const fs = require('node:fs/promises');
const path = require('node:path');
const { createWriteStream } = require('node:fs');
const { Readable, Transform } = require('node:stream');
const { pipeline } = require('node:stream/promises');

const STORE_URL = 'https://apps.gravitas.cool';
const platforms = { darwin: 'macos', win32: 'windows', linux: 'linux' };
function target(platform = process.platform, arch = process.arch) {
  if (!platforms[platform] || !['x64', 'arm64'].includes(arch)) throw new Error('This operating system or processor is not supported.');
  return { platform: platforms[platform], arch };
}
function validID(id) { return typeof id === 'string' && /^[a-z][a-z0-9-]{0,63}$/.test(id); }
function artifactFor(app, current = target()) {
  if (!validID(app.id)) throw new Error('Invalid app identity.');
  const release = app.desktopRelease;
  if (!release || !Number.isSafeInteger(release.versionCode) || release.versionCode < 1 || !/^\d+\.\d+\.\d+$/.test(release.version)) throw new Error('Invalid release version.');
  const artifacts = release.artifacts.filter(a => a.platform === current.platform && a.arch === current.arch);
  if (artifacts.length !== 1) throw new Error('No compatible release for this computer.');
  const a = artifacts[0];
  const safeName = name => typeof name === 'string' && /^[A-Za-z0-9][A-Za-z0-9._ -]{0,180}$/.test(name);
  if (!safeName(a.file) || !safeName(a.entry) || !/^[a-f0-9]{64}$/.test(a.sha256) || !Number.isSafeInteger(a.size) || a.size < 1 || a.size > 2 ** 31) throw new Error('Invalid download metadata.');
  if (a.platform === 'linux' ? !a.file.endsWith('.AppImage') || a.entry !== a.file : !(a.file.endsWith('.zip') || a.file.endsWith(a.platform === 'macos' ? '.dmg' : '.exe')) || !a.entry.endsWith(a.platform === 'macos' ? '.app' : '.exe')) throw new Error('Unsupported desktop package.');
  return a;
}
function updateAvailable(app, installed) {
  return !!installed && app.desktopRelease?.versionCode > installed.versionCode;
}
async function atomicJSON(file, data) {
  await fs.mkdir(path.dirname(file), { recursive: true, mode: 0o700 });
  const tmp = file + '.' + require('node:crypto').randomUUID() + '.tmp';
  await fs.writeFile(tmp, JSON.stringify(data, null, 2), { mode: 0o600 });
  await fs.rename(tmp, file);
}
async function readJSON(file, fallback) {
  try { return JSON.parse(await fs.readFile(file, 'utf8')); }
  catch (e) { if (e.code === 'ENOENT') return fallback; throw new Error('Saved store data could not be read. Restore it before continuing.'); }
}
async function downloadVerified(response, destination, artifact, progress = () => {}) {
  if (!response.ok || !response.body) throw new Error(`Download failed (${response.status}).`);
  const length = response.headers.get('content-length');
  if (length && Number(length) !== artifact.size) throw new Error('Download size changed. Check for updates again.');
  let received = 0;
  const hash = createHash('sha256');
  const check = new Transform({ transform(chunk, encoding, done) {
    received += chunk.length;
    if (received > artifact.size) return done(new Error('Download exceeded its verified size.'));
    hash.update(chunk); progress(received / artifact.size); done(null, chunk);
  } });
  try {
    await pipeline(Readable.fromWeb(response.body), check, createWriteStream(destination, { flags: 'wx', mode: 0o600 }));
    if (received !== artifact.size || hash.digest('hex') !== artifact.sha256) throw new Error('Download checksum verification failed.');
  } catch (e) { await fs.rm(destination, { force: true }); throw e; }
}
function releasePath(app, current = target()) {
  artifactFor(app, current);
  return `/v1/desktop/releases/${app.id}/${app.desktopRelease.versionCode}/${current.platform}/${current.arch}`;
}
module.exports = { STORE_URL, target, validID, artifactFor, updateAvailable, atomicJSON, readJSON, downloadVerified, releasePath };
