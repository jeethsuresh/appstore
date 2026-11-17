'use strict';

const { execFileSync } = require('node:child_process');
const { createRequire } = require('node:module');
const path = require('node:path');

// Use the same locked Electron Builder keychain setup and certificate-chain
// installation that signs Bazaar's own macOS packages.
const helperPath = require.resolve('../desktop/node_modules/app-builder-lib/out/codeSign/macCodeSign.js');
const { createKeychain, findIdentity, removeKeychain } = require(helperPath);
const { TmpDir } = createRequire(helperPath)('temp-file');

async function sign() {
  const app = path.resolve(process.argv[2]);
  const tmpDir = new TmpDir();
  let keychainFile;
  try {
    ({ keychainFile } = await createKeychain({
      tmpDir,
      currentDir: app,
      cscLink: process.env.MAC_CERTIFICATE_P12_BASE64,
      cscKeyPassword: process.env.MAC_CERTIFICATE_PASSWORD,
    }));
    const identity = await findIdentity('Developer ID Application', '(WV3J3848JR)', keychainFile);
    if (!identity?.name.startsWith('Developer ID Application:') || !/^[A-F0-9]{40}$/i.test(identity.hash)) {
      throw new Error('Bazaar Developer ID Application identity is unavailable.');
    }
    const signArgs = ['--keychain', keychainFile, '--force', '--timestamp', '--options', 'runtime', '--sign', identity.hash];
    execFileSync('/usr/bin/codesign', [...signArgs, path.join(app, 'Contents/Frameworks/libthornwake_core.dylib')], { stdio: 'inherit' });
    execFileSync('/usr/bin/codesign', [...signArgs, app], { stdio: 'inherit' });
  } finally {
    if (keychainFile) await removeKeychain(keychainFile);
    await tmpDir.cleanup();
  }
}

sign().catch(error => {
  console.error(error.message);
  process.exitCode = 1;
});
