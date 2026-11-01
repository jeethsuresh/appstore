'use strict';
// Desktop IPC is a same-OS-user channel, not Android package attestation. Every
// request needs foreground approval and IAM's existing target consent.
const http = require('node:http');
const path = require('node:path');
const os = require('node:os');
const { randomBytes } = require('node:crypto');
const { atomicJSON } = require('./core.cjs');
const ISSUER = 'https://gravitas.cool', STORE = 'https://apps.gravitas.cool';
const proof = value => typeof value === 'string' && /^[A-Za-z0-9_-]{43}$/.test(value);
class BrokerError extends Error { constructor(status) { super(status); this.status = status; } }
function discoveryPath(platform = process.platform, env = process.env, home = os.homedir()) {
  const root = platform === 'win32' ? env.APPDATA : platform === 'darwin' ? path.join(home, 'Library', 'Application Support') : env.XDG_DATA_HOME || path.join(home, '.local', 'share');
  if (!root || !path.isAbsolute(root)) throw new Error('User application data directory is unavailable.');
  return path.join(root, 'Gravitas', 'Private Store', 'broker-v1.json');
}
function parseRequest(value) {
  if (!value || Object.keys(value).some(k => !['protocolVersion', 'appOrigin', 'codeChallenge', 'state', 'username'].includes(k)) || value.protocolVersion !== 1 || !proof(value.codeChallenge) || !proof(value.state)) throw new BrokerError('invalid_request');
  let origin;
  try { origin = new URL(value.appOrigin); } catch { throw new BrokerError('invalid_request'); }
  if (origin.protocol !== 'https:' || origin.origin !== value.appOrigin || origin.username || origin.password || origin.port || value.appOrigin === STORE || /[\s\x00-\x1f]/.test(value.appOrigin)) throw new BrokerError('invalid_request');
  if (value.username !== undefined && (typeof value.username !== 'string' || value.username.length > 256 || /[\x00-\x1f]/.test(value.username))) throw new BrokerError('invalid_request');
  return { ...value, redirectUri: value.appOrigin + '/auth/gravitas/callback' };
}
function brokerLoginFor(username) {
  const value = username.trim().toLowerCase();
  const domain = value.startsWith('@') && value.includes(':') ? value.slice(value.indexOf(':') + 1) : value.includes('@') ? value.slice(value.lastIndexOf('@') + 1) : 'gravitas.cool';
  return domain === 'gravitas.cool';
}
function validateSession(s) {
  const now = Math.floor(Date.now() / 1000);
  if (!s || s.issuer !== ISSUER || s.appOrigin !== STORE || !s.subject || !s.account || !proof(s.accessToken) || !proof(s.refreshToken) || !proof(s.sessionCheckToken) || !s.sessionId || (typeof s.scope !== 'string' || s.scope.split(' ').length !== 2 || !s.scope.split(' ').includes('profile.name') || !s.scope.split(' ').includes('native.broker')) || !Number.isSafeInteger(s.sessionExpiresAt) || s.sessionExpiresAt <= now || s.sessionExpiresAt > now + 2592060 || !Number.isSafeInteger(s.accessExpiresAt) || s.accessExpiresAt > now + 3660 || s.accessExpiresAt > s.sessionExpiresAt) throw new BrokerError('login_required');
  return s;
}
class BrokerEngine {
  constructor({ fetch, getCredential, replaceCredential, approve }) {
    Object.assign(this, { fetch, getCredential, replaceCredential, approve });
    this.queue = Promise.resolve();
  }
  async json(route, token, body, form = false) {
    let response;
    try {
      response = await this.fetch(ISSUER + route, { method: body === undefined ? 'GET' : 'POST', redirect: 'error', signal: AbortSignal.timeout(15000), headers: { Accept: 'application/json', ...(token ? { Authorization: 'Bearer ' + token } : {}), ...(body === undefined ? {} : { 'Content-Type': form ? 'application/x-www-form-urlencoded' : 'application/json' }) }, body: body === undefined ? undefined : form ? body.toString() : JSON.stringify(body) });
      const raw = await response.text();
      if (raw.length > 65536) throw new Error();
      return { status: response.status, data: JSON.parse(raw) };
    } catch { throw new BrokerError('unavailable'); }
  }
  async discovery() {
    const r = await this.json('/.well-known/iam-configuration');
    const d = r.data;
    if (r.status !== 200 || d.issuer !== ISSUER || d.identity_domain !== 'gravitas.cool' || d.token_endpoint !== ISSUER + '/oauth/token' || d.session_check_endpoint !== ISSUER + '/oauth/session' || d.native_broker_authorization_endpoint !== ISSUER + '/oauth/broker/authorize') throw new BrokerError('unavailable');
  }
  async status(s) {
    const r = await this.json('/oauth/session', s.sessionCheckToken);
    if (r.status === 401) throw new BrokerError('login_required');
    const d = r.data;
    if (r.status !== 200 || d.active !== true || d.iss !== s.issuer || d.sub !== s.subject || d.app_origin !== STORE || d.session_id !== s.sessionId || d.exp !== s.sessionExpiresAt || d.exp <= Date.now() / 1000) throw new BrokerError('unavailable');
  }
  async rotate(credential) {
    const s = credential.broker;
    const r = await this.json('/oauth/token', null, new URLSearchParams({ grant_type: 'refresh_token', refresh_token: s.refreshToken }), true);
    if ([400, 401].includes(r.status)) throw new BrokerError('login_required');
    const d = r.data, now = Math.floor(Date.now() / 1000);
    if (r.status !== 200 || d.token_type?.toLowerCase() !== 'bearer' || d.session_id !== s.sessionId || !Number.isSafeInteger(d.expires_in) || d.expires_in < 1 || d.expires_in > 3600 || !Number.isSafeInteger(d.session_check_expires_in) || Math.abs(s.sessionExpiresAt - now - d.session_check_expires_in) > 60) throw new BrokerError('unavailable');
    const next = { ...credential, broker: validateSession({ ...s, accessToken: d.access_token, refreshToken: d.refresh_token, sessionCheckToken: d.session_check_token, scope: d.scope, accessExpiresAt: Math.min(now + d.expires_in, s.sessionExpiresAt) }) };
    // Refresh is one-use. Persist the rotated pair before further network calls.
    if (!await this.replaceCredential(credential, next)) throw new BrokerError('cancelled');
    return next;
  }
  authorize(input, signal) {
    const operation = this.queue.catch(() => {}).then(() => this.run(parseRequest(input), signal));
    this.queue = operation;
    return operation;
  }
  async run(request, signal) {
    let credential = this.getCredential();
    const s = validateSession(credential?.broker);
    if (request.username && ![s.account, s.account.replace(/^@/, ''), s.account.replace(/^@/, '').split(':')[0], s.account.replace(/^@/, '').replace(':', '@')].includes(request.username)) throw new BrokerError('login_required');
    await this.discovery();
    if (s.accessExpiresAt <= Date.now() / 1000 + 60) credential = await this.rotate(credential);
    await this.status(credential.broker);
    if (signal?.aborted || this.getCredential() !== credential) throw new BrokerError('cancelled');
    if (!await this.approve({ appOrigin: request.appOrigin, account: s.account })) throw new BrokerError('cancelled');
    if (signal?.aborted || this.getCredential() !== credential) throw new BrokerError('cancelled');
    const body = { app_origin: request.appOrigin, redirect_uri: request.redirectUri, scope: 'profile.name', code_challenge_method: 'S256', code_challenge: request.codeChallenge, state: request.state };
    let r = await this.json('/oauth/broker/authorize', credential.broker.accessToken, body);
    if (r.status === 401) { credential = await this.rotate(credential); r = await this.json('/oauth/broker/authorize', credential.broker.accessToken, body); }
    if (r.status === 401) throw new BrokerError('login_required');
    if (r.status === 403) throw new BrokerError(r.data.error === 'consent_required' ? 'consent_required' : 'unauthorized_client');
    if (r.status !== 200) throw new BrokerError('unavailable');
    const d = r.data;
    if (signal?.aborted || this.getCredential() !== credential) throw new BrokerError('cancelled');
    if (d.issuer !== ISSUER || d.app_origin !== request.appOrigin || d.redirect_uri !== request.redirectUri || d.state !== request.state || d.session_id !== s.sessionId || d.sub !== s.subject || !proof(d.code) || !Number.isSafeInteger(d.expires_in) || d.expires_in < 1 || d.expires_in > 60) throw new BrokerError('unavailable');
    return { protocolVersion: 1, status: 'authorized', issuer: ISSUER, appOrigin: request.appOrigin, redirectUri: request.redirectUri, codeChallenge: request.codeChallenge, state: request.state, code: d.code, sub: d.sub, session_id: d.session_id, expires_in: d.expires_in };
  }
}
async function serveBroker({ file = discoveryPath(), launch, authorize }) {
  const nonce = randomBytes(32).toString('base64url');
  let pending = false;
  const server = http.createServer(async (req, res) => {
    res.setHeader('Cache-Control', 'no-store'); res.setHeader('Content-Type', 'application/json');
    const reply = (status, body) => { res.writeHead(status); res.end(JSON.stringify(body)); };
    if (req.method !== 'POST' || req.url !== '/v1/authorize' || req.headers.host !== `127.0.0.1:${server.address().port}` || req.headers.origin !== undefined || req.headers.authorization !== 'Bearer ' + nonce || req.headers['content-type'] !== 'application/json') { reply(403, { status: 'invalid_request' }); return; }
    if (pending) { reply(429, { status: 'unavailable' }); return; }
    pending = true;
    const abort = new AbortController();
    res.on('close', () => { if (!res.writableFinished) abort.abort(); });
    const timeout = setTimeout(() => { abort.abort(); res.destroy(); }, 90000);
    try {
      let bytes = 0, chunks = [];
      for await (const chunk of req) { bytes += chunk.length; if (bytes > 8192) throw new BrokerError('invalid_request'); chunks.push(chunk); }
      let input;
      try { input = JSON.parse(Buffer.concat(chunks)); } catch { throw new BrokerError('invalid_request'); }
      parseRequest(input);
      const result = await authorize(input, abort.signal);
      if (!abort.signal.aborted) reply(200, result);
    } catch (e) { if (!res.destroyed) reply(200, { protocolVersion: 1, status: e instanceof BrokerError ? e.status : 'unavailable' }); }
    finally { clearTimeout(timeout); pending = false; }
  });
  server.requestTimeout = 15000; server.headersTimeout = 10000;
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const descriptor = { protocolVersion: 1, endpoint: `http://127.0.0.1:${server.address().port}/v1/authorize`, nonce, launch };
  await atomicJSON(file, descriptor);
  return { server, descriptor };
}
module.exports = { ISSUER, STORE, BrokerError, BrokerEngine, discoveryPath, parseRequest, validateSession, serveBroker, brokerLoginFor };
