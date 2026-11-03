'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { randomBytes, createHash } = require('node:crypto');
const { BrokerEngine, BrokerError, parseRequest, serveBroker, discoveryPath, brokerLoginFor } = require('../broker.cjs');
const { authorizeDesktopStore, StoreSignInCancelled } = require('../sdk/desktop-store-broker.cjs');
const proof = () => randomBytes(32).toString('base64url');
const origin = 'https://spacetime.gravitas.cool';
function input() { return { protocolVersion: 1, appOrigin: origin, codeChallenge: proof(), state: proof() }; }
function fixture(options = {}) {
  const now = Math.floor(Date.now() / 1000);
  let credential = { token: 'f'.repeat(64), broker: { issuer: 'https://gravitas.cool', appOrigin: 'https://apps.gravitas.cool', account: '@alice:gravitas.cool', subject: 'subject', sessionId: '11111111-1111-4111-8111-111111111111', accessToken: proof(), refreshToken: proof(), sessionCheckToken: proof(), scope: 'profile.name profile.username native.broker', accessExpiresAt: now + 1000, sessionExpiresAt: now + 10000 } };
  const calls = [], grants = new Map();
  let approvals = 0, rotations = 0;
  const fetch = async (url, init) => {
    const route = new URL(url).pathname; calls.push({ route, init });
    if (options.response) { const custom = options.response(route, init); if (custom) return custom; }
    const s = credential.broker;
    if (route === '/.well-known/iam-configuration') return Response.json({ issuer: s.issuer, identity_domain: 'gravitas.cool', token_endpoint: s.issuer + '/oauth/token', session_check_endpoint: s.issuer + '/oauth/session', native_broker_authorization_endpoint: s.issuer + '/oauth/broker/authorize' });
    if (route === '/oauth/session') return Response.json({ active: true, iss: s.issuer, sub: s.subject, app_origin: s.appOrigin, session_id: s.sessionId, exp: s.sessionExpiresAt });
    if (route === '/oauth/token') {
      rotations++;
      assert.equal(new URLSearchParams(init.body).get('refresh_token'), s.refreshToken);
      return Response.json({ token_type: 'Bearer', access_token: proof(), refresh_token: proof(), session_check_token: proof(), scope: s.scope, session_id: s.sessionId, expires_in: 3600, session_check_expires_in: s.sessionExpiresAt - Math.floor(Date.now() / 1000) });
    }
    assert.equal(route, '/oauth/broker/authorize');
    assert.equal(init.headers.Authorization, 'Bearer ' + s.accessToken);
    const request = JSON.parse(init.body), code = proof();
    assert.equal(request.scope, 'profile.name');
    assert.equal(request.redirect_uri, request.app_origin + '/auth/gravitas/callback');
    grants.set(code, request);
    return Response.json({ code, state: request.state, issuer: s.issuer, app_origin: request.app_origin, redirect_uri: request.redirect_uri, session_id: s.sessionId, sub: s.subject, expires_in: 60 });
  };
  const engine = new BrokerEngine({ fetch, getCredential: () => credential, replaceCredential: async (old, next) => { if (credential !== old) return false; credential = next; return true; }, approve: async value => { approvals++; assert.equal(value.account, '@alice:gravitas.cool'); return options.approve ? options.approve(value) : true; } });
  return { engine, calls, grants, get credential() { return credential; }, set credential(value) { credential = value; }, get approvals() { return approvals; }, get rotations() { return rotations; } };
}
const rejects = (p, status) => assert.rejects(p, e => e instanceof BrokerError && e.status === status);
test('new HTTPS services need only their origin; caller cannot select scope or callback', () => {
  assert.equal(parseRequest({ ...input(), appOrigin: 'https://new-service.example' }).redirectUri, 'https://new-service.example/auth/gravitas/callback');
  for (const appOrigin of ['http://app.example', 'https://app.example/', 'https://user@app.example', 'https://app.example:444', 'https://apps.gravitas.cool']) assert.throws(() => parseRequest({ ...input(), appOrigin }));
  for (const patch of [{ scope: 'native.broker' }, { redirectUri: 'https://evil.example' }, { state: 'short' }, { codeChallenge: 'short' }, { username: 'a\nb' }]) assert.throws(() => parseRequest({ ...input(), ...patch }));
});
test('approved code is bound to child verifier, target, state and family; never returns Store credentials', async () => {
  const f = fixture(), verifier = proof(), request = { ...input(), codeChallenge: createHash('sha256').update(verifier).digest('base64url') };
  const result = await f.engine.authorize(request);
  assert.equal(result.state, request.state); assert.equal(result.codeChallenge, request.codeChallenge);
  assert.equal(f.grants.get(result.code).code_challenge, request.codeChallenge);
  const wire = JSON.stringify(result);
  for (const secret of [f.credential.token, f.credential.broker.accessToken, f.credential.broker.refreshToken, f.credential.broker.sessionCheckToken, verifier]) assert.ok(!wire.includes(secret));
  assert.equal(f.approvals, 1);
});
test('cancellation, wrong account and missing consent never yield codes', async () => {
  const declined = fixture({ approve: async () => false }); await rejects(declined.engine.authorize(input()), 'cancelled'); assert.equal(declined.grants.size, 0);
  const wrong = fixture(); await rejects(wrong.engine.authorize({ ...input(), username: 'bob' }), 'login_required'); assert.equal(wrong.approvals, 0);
  const consent = fixture({ response: route => route === '/oauth/broker/authorize' ? Response.json({ error: 'consent_required' }, { status: 403 }) : null }); await rejects(consent.engine.authorize(input()), 'consent_required');
});
test('expired access rotates once, persists before status and serializes concurrent requests', async () => {
  const f = fixture(); f.credential.broker.accessExpiresAt = 1;
  const results = await Promise.all([f.engine.authorize(input()), f.engine.authorize(input())]);
  assert.equal(f.rotations, 1); assert.equal(f.approvals, 2); assert.notEqual(results[0].code, results[1].code);
  assert.ok(f.calls.findIndex(x => x.route === '/oauth/token') < f.calls.findIndex(x => x.route === '/oauth/session'));
});
test('revoked family, forged discovery and changed returned binding fail closed', async () => {
  const revoked = fixture({ response: route => route === '/oauth/session' ? Response.json({ error: 'invalid_token' }, { status: 401 }) : null }); await rejects(revoked.engine.authorize(input()), 'login_required'); assert.equal(revoked.approvals, 0);
  const forged = fixture({ response: route => route.includes('.well-known') ? Response.json({ issuer: 'https://evil.example' }) : null }); await rejects(forged.engine.authorize(input()), 'unavailable');
  const changed = fixture({ response: route => route === '/oauth/broker/authorize' ? Response.json({ code: proof(), state: 'forged' }) : null }); await rejects(changed.engine.authorize(input()), 'unavailable');
});
test('sign-out/account change or caller disconnect during approval discards the result', async () => {
  let release, entered;
  const ready = new Promise(r => { entered = r; });
  const f = fixture({ approve: () => { entered(); return new Promise(r => { release = r; }); } });
  const pending = f.engine.authorize(input()); await ready;
  f.credential = null; release(true); await rejects(pending, 'cancelled'); assert.equal(f.grants.size, 0);
  const controller = new AbortController(), aborted = fixture({ approve: async () => { controller.abort(); return true; } });
  await rejects(aborted.engine.authorize(input(), controller.signal), 'cancelled'); assert.equal(aborted.grants.size, 0);
});
test('authenticated loopback IPC rejects browser origins, wrong hosts/nonces, extra fields and large bodies', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'broker-')); t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const f = fixture(), file = path.join(dir, 'broker.json');
  const { server, descriptor: d } = await serveBroker({ file, launch: { file: process.execPath, args: [] }, authorize: (...args) => f.engine.authorize(...args) });
  t.after(() => { server.closeAllConnections(); server.close(); });
  // Windows uses the user profile's inherited ACL, not POSIX group/mode bits.
  if (process.platform !== 'win32') assert.equal((await fs.stat(file)).mode & 0o077, 0);
  const send = (headers, body = input()) => fetch(d.endpoint, { method: 'POST', headers: { Authorization: 'Bearer ' + d.nonce, 'Content-Type': 'application/json', ...headers }, body: JSON.stringify(body) });
  for (const headers of [{ Authorization: 'Bearer bad' }, { Origin: 'https://evil.example' }, { Origin: 'null' }]) assert.equal((await send(headers)).status, 403);
  const hostStatus = await new Promise((resolve, reject) => { const req = require('node:http').request(d.endpoint, { method: 'POST', headers: { Host: 'evil.example', Authorization: 'Bearer ' + d.nonce, 'Content-Type': 'application/json' } }, res => { res.resume(); resolve(res.statusCode); }); req.on('error', reject); req.end(JSON.stringify(input())); });
  assert.equal(hostStatus, 403);
  assert.equal((await (await send({}, { ...input(), scope: 'all' })).json()).status, 'invalid_request');
  assert.equal((await (await send({}, { ...input(), extra: 'x'.repeat(9000) })).json()).status, 'invalid_request');
  assert.equal(f.calls.length, 0);
  const result = await authorizeDesktopStore({ appOrigin: origin, challenge: proof(), state: proof(), file }); assert.equal(result.status, 'authorized');
});
test('helper honors cancellation and validates all response bindings', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'broker-client-')), file = path.join(dir, 'broker.json'); t.after(() => fs.rm(dir, { recursive: true, force: true }));
  let mode = 'cancelled';
  const { server } = await serveBroker({ file, launch: { file: process.execPath, args: [] }, authorize: async r => ({ protocolVersion: 1, status: mode, issuer: 'https://evil.example', appOrigin: r.appOrigin, state: r.state }) });
  t.after(() => { server.closeAllConnections(); server.close(); });
  const request = { appOrigin: origin, challenge: proof(), state: proof(), file };
  await assert.rejects(authorizeDesktopStore(request), StoreSignInCancelled);
  mode = 'authorized'; assert.equal(await authorizeDesktopStore(request), null);
  assert.equal(await authorizeDesktopStore({ ...request, file: path.join(dir, 'missing') }), null);
});
test('helper wakes a closed Store and rereads the fresh endpoint and nonce', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'broker-cold-')), file = path.join(dir, 'broker.json'); t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const f = fixture();
  const old = await serveBroker({ file, launch: { file: process.execPath, args: [] }, authorize: (...args) => f.engine.authorize(...args) });
  await new Promise(r => old.server.close(r));
  let wakes = 0, next;
  const result = await authorizeDesktopStore({ appOrigin: origin, challenge: proof(), state: proof(), file, launch: async (executable, args) => { wakes++; assert.equal(executable, process.execPath); assert.deepEqual(args, []); next = await serveBroker({ file, launch: { file: process.execPath, args: [] }, authorize: (...args) => f.engine.authorize(...args) }); } });
  t.after(() => { next.server.closeAllConnections(); next.server.close(); });
  assert.equal(result.status, 'authorized'); assert.equal(wakes, 1); assert.notEqual(old.descriptor.nonce, next.descriptor.nonce);
});
test('discovery uses the current OS user application data on all platforms', () => {
  assert.equal(discoveryPath('linux', { XDG_DATA_HOME: '/data' }, '/home/user'), path.join('/data', 'Gravitas', 'Private Store', 'broker-v1.json'));
  assert.equal(discoveryPath('darwin', {}, '/Users/user'), path.join('/Users/user/Library/Application Support', 'Gravitas', 'Private Store', 'broker-v1.json'));
  assert.equal(discoveryPath('win32', { APPDATA: '/AppData/Roaming' }, '/Users/user'), path.join('/AppData/Roaming', 'Gravitas', 'Private Store', 'broker-v1.json'));
});
test('cold-start helper spawns a separate Store process without a shell', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'broker-spawn-')), file = path.join(dir, 'broker.json'), script = path.join(dir, 'fixture.cjs');
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const old = await serveBroker({ file, launch: { file: process.execPath, args: [script] }, authorize: async () => ({ status: 'unavailable' }) });
  await new Promise(resolve => old.server.close(resolve));
  await fs.writeFile(script, `const {serveBroker}=require(${JSON.stringify(path.resolve(__dirname,'../broker.cjs'))});
serveBroker({file:${JSON.stringify(file)},launch:{file:process.execPath,args:[__filename]},authorize:async r=>({protocolVersion:1,status:'authorized',issuer:'https://gravitas.cool',appOrigin:r.appOrigin,redirectUri:r.appOrigin+'/auth/gravitas/callback',state:r.state,codeChallenge:r.codeChallenge,code:'c'.repeat(43),sub:'alice',session_id:'family',expires_in:60})}).then(()=>{setTimeout(()=>process.exit(0),2000)});`);
  const result = await authorizeDesktopStore({ appOrigin: origin, challenge: proof(), state: proof(), file });
  assert.equal(result.status, 'authorized');
});

test('Store retains profile-only login for other identity domains', () => {
  for (const user of ['alice', 'alice@gravitas.cool', '@alice:gravitas.cool']) assert.equal(brokerLoginFor(user), true);
  for (const user of ['alice@remote.example', '@alice:remote.example']) assert.equal(brokerLoginFor(user), false);
});
test('server cancellation discards even a late successful authorization response', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'broker-cancel-')), file = path.join(dir, 'broker.json');
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  let entered, finish;
  const ready = new Promise(resolve => { entered = resolve; });
  const ipc = await serveBroker({ file, launch: { file: process.execPath, args: [] }, authorize: async request => {
    entered(); await new Promise(resolve => { finish = resolve; });
    return { protocolVersion: 1, status: 'authorized', issuer: 'https://gravitas.cool', appOrigin: request.appOrigin, redirectUri: request.appOrigin + '/auth/gravitas/callback', state: request.state, codeChallenge: request.codeChallenge, code: 'c'.repeat(43), sub: 'alice', session_id: 'family', expires_in: 60 };
  } });
  t.after(() => { ipc.server.closeAllConnections(); ipc.server.close(); });
  const result = authorizeDesktopStore({ appOrigin: origin, challenge: proof(), state: proof(), file }).then(value => ({ value }), error => ({ error }));
  await ready; ipc.cancelPending(); finish();
  assert.ok((await result).error instanceof StoreSignInCancelled);
});
