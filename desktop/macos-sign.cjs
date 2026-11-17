'use strict';
const fs = require('node:fs');

// Thin and universal Mach-O, in both byte orders, including 64-bit fat headers.
const MACH_O = new Set([0xfeedface, 0xcefaedfe, 0xfeedfacf, 0xcffaedfe, 0xcafebabe, 0xbebafeca, 0xcafebabf, 0xbfbafeca]);
function isMachO(file) {
  const descriptor = fs.openSync(file, 'r');
  try {
    const header = Buffer.alloc(4);
    return fs.readSync(descriptor, header, 0, 4, 0) === 4 && MACH_O.has(header.readUInt32BE());
  } finally { fs.closeSync(descriptor); }
}

function signingOptions(options, release = Boolean(process.env.CSC_LINK)) {
  if (release && (!/^[a-f0-9]{40}$/i.test(options.identity || '') || !options.keychain)) {
    throw new Error('A macOS release requires the selected Developer ID keychain identity.');
  }
  const original = options.ignore == null ? [] : Array.isArray(options.ignore) ? options.ignore : [options.ignore];
  return {
    ...options,
    strictVerify: true,
    ignore: [...original, file => {
      // The app/framework signatures seal data resources. Timestamp and sign
      // executable code and nested bundles, rather than each binary data file.
      // Reading errors fail signing instead of silently skipping a code file.
      return fs.statSync(file).isFile() && !isMachO(file);
    }],
  };
}

async function signMacOS(options) {
  // Retain Builder's keychain, exact identity hash, entitlements, runtime flags,
  // secure timestamp options and retry behavior. Builder notarizes afterward.
  const { sign } = require('app-builder-lib/out/codeSign/macCodeSign.js');
  return sign(signingOptions(options));
}
module.exports = signMacOS;
module.exports.isMachO = isMachO;
module.exports.signingOptions = signingOptions;
