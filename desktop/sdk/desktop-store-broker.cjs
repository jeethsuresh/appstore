'use strict';
// Main-process helper for Electron apps. App backend exchanges the returned
// code with its retained verifier; no Store credential reaches the renderer.
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { spawn } = require('node:child_process');
function discoveryPath() {
  const root = process.platform === 'win32' ? process.env.APPDATA : process.platform === 'darwin' ? path.join(os.homedir(), 'Library', 'Application Support') : process.env.XDG_DATA_HOME || path.join(os.homedir(), '.local', 'share');
  return path.join(root, 'Gravitas', 'Private Store', 'broker-v1.json');
}
class StoreSignInCancelled extends Error { constructor() { super('Sign-in cancelled.'); } }
async function authorizeDesktopStore({ appOrigin, challenge, state, username, file = discoveryPath(), launch, signal }) {
  const proof = s => typeof s === 'string' && /^[A-Za-z0-9_-]{43}$/.test(s);
  if (!proof(challenge) || !proof(state)) throw new Error('Invalid sign-in proof.');
  const read = async () => {
    if ((await fs.stat(file)).size > 8192) throw new Error('Invalid discovery');
    return JSON.parse(await fs.readFile(file, 'utf8'));
  };
  const request = async d => {
    const url = new URL(d.endpoint);
    if (d.protocolVersion !== 1 || !proof(d.nonce) || url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || Number(url.port) < 1024 || url.pathname !== '/v1/authorize' || url.username || url.password || url.search || url.hash) throw new Error('Invalid discovery');
    const response = await fetch(url, { method: 'POST', redirect: 'error', signal: AbortSignal.any([AbortSignal.timeout(95000), ...(signal ? [signal] : [])]), headers: { Authorization: 'Bearer ' + d.nonce, 'Content-Type': 'application/json' }, body: JSON.stringify({ protocolVersion: 1, appOrigin, codeChallenge: challenge, state, ...(username ? { username } : {}) }) });
    if (!response.ok) return null;
    const text = await response.text();
    if (text.length > 8192) return null;
    return JSON.parse(text);
  };
  try {
    let d = await read(), result;
    try { result = await request(d); }
    catch (e) {
      // Only an absent listener causes a launch, never a rejected HTTP request.
      if (e.cause?.code !== 'ECONNREFUSED' || signal?.aborted) return null;
      if (!path.isAbsolute(d.launch.file) || !Array.isArray(d.launch.args) || d.launch.args.length > 1 || d.launch.args.some(arg => !path.isAbsolute(arg))) return null;
      if (launch) await launch(d.launch.file, d.launch.args);
      else await new Promise((resolve, reject) => { const p = spawn(d.launch.file, d.launch.args, { detached: true, stdio: 'ignore' }); p.once('error', reject); p.once('spawn', () => { p.unref(); resolve(); }); });
      const deadline = Date.now() + 20000;
      while (Date.now() < deadline && !signal?.aborted) {
        await new Promise(resolve => setTimeout(resolve, 250));
        d = await read();
        try { result = await request(d); break; } catch (retry) { if (retry.cause?.code !== 'ECONNREFUSED') return null; }
      }
    }
    if (signal?.aborted) throw new StoreSignInCancelled();
    if (result?.status === 'cancelled') throw new StoreSignInCancelled();
    if (result?.status !== 'authorized') return null;
    if (result.protocolVersion !== 1 || result.issuer !== 'https://gravitas.cool' || result.appOrigin !== appOrigin || result.redirectUri !== appOrigin + '/auth/gravitas/callback' || result.state !== state || result.codeChallenge !== challenge || !proof(result.code) || typeof result.sub !== 'string' || !result.sub || typeof result.session_id !== 'string' || !result.session_id || !Number.isSafeInteger(result.expires_in) || result.expires_in < 1 || result.expires_in > 60) return null;
    return result;
  } catch (e) { if (e instanceof StoreSignInCancelled) throw e; return null; }
}
module.exports = { authorizeDesktopStore, discoveryPath, StoreSignInCancelled };
