'use strict';
const fs = (process.versions.electron ? require('original-fs') : require('node:fs')).promises;
const path = require('node:path');

async function requireFreeSpace(directory, bytes, phase) {
  let volume;
  try { volume = await fs.statfs(directory, { bigint: true }); }
  catch (e) {
    if (e.code === 'ENOSYS' || e.code === 'ENOTSUP') return;
    throw e;
  }
  const available = volume.bavail * volume.bsize;
  const missing = BigInt(bytes) - available;
  if (missing > 0n) {
    const mib = 1024n ** 2n;
    throw new Error(`Not enough disk space to ${phase}. Free at least ${((missing + mib - 1n) / mib)} MiB and try again.`);
  }
}

function installError(error) {
  if (error.code !== 'ENOSPC' && error.code !== 'EDQUOT') return error;
  return new Error('Bazaar ran out of disk space. Free some space on this computer and try again.', { cause: error });
}

// Electron's recursive rm may still interpret app.asar as a directory even
// through original-fs. Use non-recursive filesystem calls for staging cleanup.
async function removeStaging(directory) {
  try {
    if (!(await fs.lstat(directory)).isDirectory()) {
      await fs.unlink(directory);
      return;
    }
    for (const file of await fs.readdir(directory)) await removeStaging(path.join(directory, file));
    await fs.rmdir(directory);
  } catch (e) { if (e.code !== 'ENOENT') throw e; }
}

module.exports = { requireFreeSpace, installError, removeStaging };
