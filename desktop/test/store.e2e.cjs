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
    await electron.evaluate(({ net, app }, digest) => {
      const bytes = '#!/bin/sh\nexit 0\n';
      const artifact = { platform: 'linux', arch: 'x64', file: 'Example.AppImage', entry: 'Example.AppImage', size: Buffer.byteLength(bytes), sha256: digest };
      global.fixtureApps = [{ id: 'example', name: 'Example', repository: 'owner/example', description: 'An independent desktop app.', desktopRelease: { schemaVersion: 1, version: '1.0.0', versionCode: 1, changes: ['A first release.'], artifacts: [artifact] } }];
      const originalFetch = net.fetch;
      net.fetch = async (url, options) => {
        if (!String(url).startsWith('https://apps.gravitas.cool/')) return originalFetch(url, options);
        if (String(url).includes('/v1/desktop/releases/')) return new Response(bytes);
        if (String(url).includes('/v1/apps?') || String(url).includes('/v1/desktop/apps?')) return Response.json({ apps: global.fixtureApps });
        return new Response('Missing fixture', { status: 404 });
      };
      app.on('browser-window-created', (_event, window) => {
        window.webContents.session.protocol.handle('https', request => request.url.includes('/v1/auth/desktop/enroll') ? Response.json({ token: 'a'.repeat(64), account: 'desktop-user', expiresAt: 9999999999 }) : new Response('<!doctype html><title>Sign in</title>Sign in with Gravitas', { headers: { 'Content-Type': 'text/html' } }));
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
    await electron.evaluate(async ({ BrowserWindow }) => { const window = BrowserWindow.getAllWindows().find(w => w.getTitle() === 'Sign in with Gravitas' || w.webContents.getURL().startsWith('https:')); await window.loadURL('https://apps.gravitas.cool/desktop/connect'); });
    await expect(page.getByText('desktop-user', { exact: true })).toBeVisible();
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
  } finally { await electron.evaluate(({ app }) => { if (global.originalQuit) app.quit = global.originalQuit; }).catch(() => {}); await electron.close(); await fs.rm(temporary, { recursive: true, force: true }); }
});
