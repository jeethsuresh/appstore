'use strict';
const fs = require('node:fs/promises');
const { createWriteStream } = require('node:fs');
const path = require('node:path');
const { pipeline } = require('node:stream/promises');
const yauzl = require('yauzl');

// Extract files first and create validated symlinks last. No archive entry can
// write through an earlier symlink, including macOS framework directory links.
async function extractZip(file, { dir }) {
  const zip = await new Promise((resolve, reject) => yauzl.open(file, { lazyEntries: true, strictFileNames: true }, (error, value) => error ? reject(error) : resolve(value)));
  const links = [], seen = new Set(); let total = 0;
  const inside = location => location.startsWith(dir + path.sep);
  try {
    await new Promise((resolve, reject) => {
      zip.once('error', reject); zip.once('end', resolve);
      zip.on('entry', entry => {
        (async () => {
          const destination = path.resolve(dir, entry.fileName);
          if (!inside(destination) || entry.fileName.includes('\\') || /^[A-Za-z]:/.test(entry.fileName) || seen.has(destination) || seen.size >= 100000) throw new Error('Unsafe or duplicate archive entry.');
          seen.add(destination); total += entry.uncompressedSize;
          if (total > 5 * 2 ** 30) throw new Error('Expanded app exceeds the installation limit.');
          const mode = entry.externalFileAttributes >>> 16, type = mode & 0o170000;
          if (type === 0o120000) {
            if (entry.uncompressedSize > 4096) throw new Error('Invalid archive symlink.');
            const stream = await new Promise((resolve, reject) => zip.openReadStream(entry, (e, stream) => e ? reject(e) : resolve(stream)));
            const buffers = []; await new Promise((resolve, reject) => { stream.on('data', chunk => buffers.push(chunk)); stream.once('end', resolve); stream.once('error', reject); });
            const target = Buffer.concat(buffers).toString('utf8');
            if (!target || target.includes('\0') || target.includes('\\') || path.isAbsolute(target) || !inside(path.resolve(path.dirname(destination), target))) throw new Error('Archive symlink leaves the app directory.');
            links.push({ destination, target });
          } else if (entry.fileName.endsWith('/')) {
            await fs.mkdir(destination, { recursive: true, mode: 0o755 });
          } else {
            if (type && type !== 0o100000) throw new Error('Unsupported archive entry.');
            await fs.mkdir(path.dirname(destination), { recursive: true, mode: 0o755 });
            const stream = await new Promise((resolve, reject) => zip.openReadStream(entry, (e, stream) => e ? reject(e) : resolve(stream)));
            await pipeline(stream, createWriteStream(destination, { flags: 'wx', mode: mode & 0o111 ? 0o755 : 0o644 }));
          }
          zip.readEntry();
        })().catch(reject);
      });
      zip.readEntry();
    });
    for (const link of links) await fs.mkdir(path.dirname(link.destination), { recursive: true });
    for (const link of links) await fs.symlink(link.target, link.destination);
    for (const link of links) if (!inside(await fs.realpath(link.destination))) throw new Error('Archive symlink resolves outside the app directory.');
  } finally { zip.close(); }
}
module.exports = extractZip;
