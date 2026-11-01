'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { CredentialVault } = require('../credential-vault.cjs');
function deferred() { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; }
test('sign-out invalidates memory immediately and defeats an in-flight refresh disk write', async () => {
  const old = { account: 'old' }, next = { account: 'refreshed' };
  let current = old, disk = old;
  const writing = deferred(), release = deferred();
  const vault = new CredentialVault({ read: () => current, commit: v => { current = v; }, persist: async v => { if (v === next) { writing.resolve(); await release.promise; } disk = v; } });
  const refresh = vault.replace(old, next);
  await writing.promise;
  const logout = vault.invalidate();
  assert.equal(logout.previous, old); assert.equal(current, null);
  release.resolve();
  assert.equal(await refresh, false);
  await logout.cleared; await vault.settled();
  assert.equal(current, null); assert.equal(disk, null);
});
test('late network refresh after logout cannot persist a token; a newer login survives', async () => {
  const old = { account: 'old' }, fresh = { account: 'fresh' };
  let current = old, disk = old;
  const vault = new CredentialVault({ read: () => current, commit: v => { current = v; }, persist: async v => { disk = v; } });
  const logout = vault.invalidate();
  await logout.cleared;
  assert.equal(await vault.replace(old, { account: 'late refresh' }), false);
  assert.equal(await vault.replace(null, fresh), true);
  assert.equal(await vault.replace(old, null), false);
  assert.equal(current, fresh); assert.equal(disk, fresh);
});
test('cancelled login restores the current credential and quit waits for queued clearing', async () => {
  const old = { account: 'old' }, next = { account: 'pending' };
  let current = old, disk = old, valid = true;
  const started = deferred(), release = deferred();
  const vault = new CredentialVault({ read: () => current, commit: v => { current = v; }, persist: async v => { if (v === next) { started.resolve(); await release.promise; } disk = v; } });
  const login = vault.replace(old, next, () => valid);
  await started.promise; valid = false; release.resolve();
  assert.equal(await login, false); assert.equal(current, old); assert.equal(disk, old);
  const logout = vault.invalidate();
  let settled = false; const quitting = vault.settled().then(() => { settled = true; });
  assert.equal(settled, false); await logout.cleared; await quitting;
  assert.equal(disk, null); assert.equal(current, null);
});
