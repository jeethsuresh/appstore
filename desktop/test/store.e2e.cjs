const { test, expect, _electron } = require('@playwright/test');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { createHash } = require('node:crypto');
const storeVersionCode = require('../package.json').versionCode;

test('desktop discovers compatible apps, signs in, installs, updates and stages its own restart', async () => {
  test.skip(process.platform !== 'linux', 'AppImage installation flow runs on Linux; platform packages are built separately.');
  const temporary = await fs.mkdtemp(path.join(os.tmpdir(), 'store-e2e-'));
  const electron = await _electron.launch({ args: ['--no-sandbox', path.resolve(__dirname, '..')], env: { ...process.env, XDG_CONFIG_HOME: temporary, XDG_DATA_HOME: path.join(temporary, 'local'), HOME: temporary } });
  try {
    const output = path.join(temporary, 'asar-extraction');
    await fs.mkdir(output);
    const extractor = path.resolve(__dirname, '../unzip.cjs');
    const archive = path.join(__dirname, 'fixtures', 'asar-file.zip');
    await electron.evaluate(async (_electron, { extractor, archive, output }) => {
      const load = process.getBuiltinModule('node:module').createRequire(extractor);
      await load(extractor)(archive, { dir: output });
    }, { extractor, archive, output });
    expect(await fs.readFile(path.join(output, 'Example.app', 'Contents', 'Resources', 'app.asar'), 'utf8')).toBe('archive bytes');

    await electron.evaluate(({ net, app, dialog }, digest) => {
      global.brokerAccount = { issuer: 'https://gravitas.cool', appOrigin: 'https://apps.gravitas.cool', account: '@desktop-user:gravitas.cool', subject: 'desktop-user', sessionId: '11111111-1111-4111-8111-111111111111', accessToken: 'a'.repeat(43), refreshToken: 'r'.repeat(43), sessionCheckToken: 'p'.repeat(43), scope: 'profile.name profile.username native.broker', accessExpiresAt: Math.floor(Date.now()/1000) + 1800, sessionExpiresAt: Math.floor(Date.now()/1000) + 10000 };
      global.brokerDecision = 1;
      dialog.showMessageBox = async (_window, options) => { global.brokerPrompt = options; return { response: global.brokerDecision }; };
      const bytes = '#!/bin/sh\nexit 0\n';
      const artifact = { platform: 'linux', arch: 'x64', file: 'Example.AppImage', entry: 'Example.AppImage', size: Buffer.byteLength(bytes), sha256: digest };
      global.fixtureApps = [{ id: 'example', name: 'Example', repository: 'owner/example', description: 'An independent desktop app.', desktopRelease: { schemaVersion: 1, version: '1.0.0', versionCode: 1, changes: ['A first release.'], artifacts: [artifact] } }];
      const originalFetch = net.fetch;
      net.fetch = async (url, options) => {
        if (String(url).startsWith('https://gravitas.cool/')) {
          const s = global.brokerAccount;
          if (String(url).includes('/.well-known/')) return Response.json({ issuer: s.issuer, identity_domain: 'gravitas.cool', token_endpoint: s.issuer + '/oauth/token', session_check_endpoint: s.issuer + '/oauth/session', native_broker_authorization_endpoint: s.issuer + '/oauth/broker/authorize' });
          if (String(url).endsWith('/oauth/session')) return Response.json({ active: true, iss: s.issuer, sub: s.subject, app_origin: s.appOrigin, session_id: s.sessionId, exp: s.sessionExpiresAt });
          if (String(url).endsWith('/oauth/broker/authorize')) { const r = JSON.parse(options.body); return Response.json({ code: 'c'.repeat(43), state: r.state, issuer: s.issuer, app_origin: r.app_origin, redirect_uri: r.redirect_uri, sub: s.subject, session_id: s.sessionId, expires_in: 60 }); }
        }
        if (!String(url).startsWith('https://apps.gravitas.cool/')) return originalFetch(url, options);
        if (String(url).endsWith('/v1/auth/gravitas/start')) { global.loginRequest = JSON.parse(options.body); return Response.json({ authorizationUrl: 'https://gravitas.cool/oauth/start', redirectUri: 'https://apps.gravitas.cool/auth/android/callback' }); }
        if (String(url).endsWith('/v1/auth/gravitas/exchange')) { global.exchangeRequest = JSON.parse(options.body); return Response.json({ token: 'a'.repeat(64), account: 'desktop-user', expiresAt: 9999999999, broker: global.brokerAccount }); }

        if (String(url).includes('/v1/desktop/releases/')) return new Response(bytes);
        if (String(url).includes('/v1/apps?') || String(url).includes('/v1/desktop/apps?')) return Response.json({ apps: global.fixtureApps });
        return new Response('Missing fixture', { status: 404 });
      };
      app.on('browser-window-created', (_event, window) => {
        window.webContents.session.protocol.handle('https', _request => new Response('<!doctype html><title>Sign in</title>Sign in with Gravitas', { headers: { 'Content-Type': 'text/html' } }));
      });
    }, createHash('sha256').update('#!/bin/sh\nexit 0\n').digest('hex'));
    const page = await electron.firstWindow();
    await page.getByRole('button', { name: 'Check for updates' }).click();
    await expect(page.getByRole('heading', { name: 'Example', exact: true })).toBeVisible();
    await page.screenshot({ path: path.join(__dirname, '..', 'test-results', 'desktop-catalog.png') });
    await expect(page.getByRole('button', { name: 'Install ↓' })).toBeDisabled();
    await page.getByRole('button', { name: 'App details' }).click();
    await expect(page.getByText('A first release.')).toBeVisible();
    await page.getByRole('button', { name: 'Back to the collection' }).click();
    await page.getByLabel('Gravitas username').fill('desktop-user');
    await page.getByRole('button', { name: 'Sign in 🔥' }).click();
    await expect.poll(async () => (await electron.windows()).length).toBe(2);
    await electron.evaluate(async ({ BrowserWindow }) => { const window = BrowserWindow.getAllWindows().find(w => w.getTitle() === 'Sign in with Gravitas' || w.webContents.getURL().startsWith('https:')); await window.loadURL('https://apps.gravitas.cool/auth/android/callback?code=fixture-code&state=' + global.loginRequest.state).catch(() => {}); });
    await expect(page.getByText('desktop-user', { exact: true })).toBeVisible();
    const signIn = await electron.evaluate(() => ({ start: global.loginRequest, exchange: global.exchangeRequest }));
    expect(signIn.start.broker).toBe(true); expect(signIn.exchange.broker).toBe(true);
    expect(createHash('sha256').update(signIn.exchange.codeVerifier).digest('base64url')).toBe(signIn.start.codeChallenge);
    expect(await page.evaluate(() => typeof process)).toBe('undefined');
    const { authorizeDesktopStore, StoreSignInCancelled } = require('../sdk/desktop-store-broker.cjs');
    const file = path.join(temporary, 'local', 'Gravitas', 'Private Store', 'broker-v1.json');
    const request = { appOrigin: 'https://spacetime.gravitas.cool', challenge: 'q'.repeat(43), state: 's'.repeat(43), file };
    const code = await authorizeDesktopStore(request);
    expect(code.status).toBe('authorized'); expect(code.code).toHaveLength(43);
    const prompt = await electron.evaluate(() => global.brokerPrompt);
    expect(prompt.message).toContain('spacetime.gravitas.cool'); expect(prompt.detail).toContain('@desktop-user:gravitas.cool');
    await electron.evaluate(() => { global.brokerDecision = 0; });
    await expect(authorizeDesktopStore(request)).rejects.toBeInstanceOf(StoreSignInCancelled);
    await electron.evaluate(() => { global.brokerDecision = 1; });

    await page.getByRole('button', { name: 'Install ↓' }).click();
    await expect(page.getByRole('button', { name: 'Open ↗' })).toBeVisible();
    await page.getByRole('button', { name: 'Installed', exact: false }).click();
    await expect(page.getByRole('heading', { name: 'Example', exact: true })).toBeVisible();
    await electron.evaluate(() => { global.fixtureApps[0].desktopRelease.versionCode = 2; global.fixtureApps[0].desktopRelease.version = '1.0.1'; });
    await page.getByRole('button', { name: 'Check for updates' }).click();
    await expect(page.getByRole('button', { name: 'Update ↓', exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Update all ↓' }).click();
    await expect(page.getByRole('button', { name: 'Open ↗' })).toBeVisible();
    await electron.evaluate((_electron, code) => { global.fixtureApps.push({ ...global.fixtureApps[0], id: 'appstore', name: 'Private Store', desktopRelease: { ...global.fixtureApps[0].desktopRelease, versionCode: code + 1, version: '9.0.0' } }); }, storeVersionCode);
    await page.getByRole('button', { name: 'Check for updates' }).click();
    await page.getByRole('button', { name: 'Update all ↓' }).click();
    await expect(page.getByText('A new Private Store is ready.', { exact: false })).toBeVisible();
    await electron.evaluate(({ app }) => { app.relaunch = options => { global.restartOptions = options; }; global.originalQuit = app.quit; app.quit = () => { global.restartRequested = true; }; });
    await page.locator('#restart-button').click();
    await expect.poll(() => electron.evaluate(() => global.restartRequested)).toBe(true);
    const options = await electron.evaluate(() => global.restartOptions);
    expect(options.execPath).toContain(path.join('appstore', String(storeVersionCode + 1)));
    expect(options.args).toEqual([]);
    await fs.access(options.execPath);
    await page.getByRole('button', { name: 'Settings', exact: false }).click();
    await expect(page.getByLabel('Automatic app updates')).toBeChecked();
    await page.getByLabel('Automatic app updates').uncheck();
    await expect(page.getByLabel('Automatic app updates')).not.toBeChecked();
    await page.screenshot({ path: path.join(__dirname, '..', 'test-results', 'desktop-settings.png') });
    // A code response and failed device revocation may arrive after sign-out.
    // Neither is allowed to revive the Store account or complete app sign-in.
    await electron.evaluate(({ net }) => {
      const original = net.fetch;
      net.fetch = async (url, options) => {
        if (String(url).endsWith('/oauth/broker/authorize')) {
          global.authorizationPending = true;
          return new Promise(resolve => { global.finishAuthorization = async () => resolve(await original(url, options)); });
        }
        if (String(url).endsWith('/v1/device') && options.method === 'DELETE') {
          global.deletingToken = options.headers.Authorization;
          global.deviceDeletePending = true;
          return new Promise(resolve => { global.finishDelete = () => resolve(Response.json({ error: 'offline' }, { status: 503 })); });
        }
        return original(url, options);
      };
    });
    const pendingSignIn = authorizeDesktopStore(request).then(value => ({ value }), error => ({ error: error.constructor.name }));
    await expect.poll(() => electron.evaluate(() => global.authorizationPending)).toBe(true);
    await page.evaluate(() => { window.signOutResult = window.store.action('logout').catch(error => error.message); });
    await expect(page.getByLabel('Gravitas username')).toBeVisible();
    await expect.poll(() => electron.evaluate(() => global.deviceDeletePending)).toBe(true);
    expect(await electron.evaluate(() => global.deletingToken)).toBe('Bearer ' + 'a'.repeat(64));
    await electron.evaluate(() => global.finishAuthorization());
    expect(await pendingSignIn).toEqual({ error: 'StoreSignInCancelled' });
    await electron.evaluate(() => global.finishDelete());
    expect(await page.evaluate(() => window.signOutResult)).toContain('Signed out on this computer');
    expect((await page.evaluate(() => window.store.action('state'))).account).toBe('');

  } finally { await electron.evaluate(({ app }) => { if (global.originalQuit) app.quit = global.originalQuit; }).catch(() => {}); await electron.close(); await fs.rm(temporary, { recursive: true, force: true }); }
});
