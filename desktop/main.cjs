'use strict';
const { app, BrowserWindow, ipcMain, protocol, net, session, safeStorage, shell, Menu, dialog } = require('electron');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { pathToFileURL } = require('node:url');
const { spawn } = require('node:child_process');
const { randomUUID, randomBytes, createHash } = require('node:crypto');
const { STORE_URL, target, validID, atomicJSON, readJSON, artifactFor, updateAvailable, downloadVerified, releasePath } = require('./core.cjs');
const credentials = require('./core.cjs');
const { installPackage, executable, createShortcut } = require('./installer.cjs');
const version = require('./package.json');
const { CredentialVault } = require('./credential-vault.cjs');
const { BrokerEngine, serveBroker, validateSession, brokerLoginFor } = require('./broker.cjs');
protocol.registerSchemesAsPrivileged([{ scheme: 'store', privileges: { standard: true, secure: true, supportFetchAPI: true } }]);
app.setName('Private Store');
if (!app.requestSingleInstanceLock()) app.quit();

let window, loginWindow, credential, catalog = [], installed = {}, settings = { autoUpdate: true }, busy = false, checking = false, error = '', progress = null, pendingSelf = null, secureStorage = false;
const current = target();
let statePath, sessionPath, root, brokerServer, brokerEngine, authGeneration = 0;
const vault = new CredentialVault({
  read: () => credential,
  commit: value => { credential = value; publish(); },
  persist: async value => {
    if (value && secureStorage) await credentials.saveCredential(sessionPath, value, safeStorage);
    else await fs.rm(sessionPath, { force: true });
  },
});
const replaceCredential = (...args) => vault.replace(...args);

function snapshot() {
  return { apps: catalog, installed: { ...installed, appstore: { version: app.getVersion(), versionCode: version.versionCode } }, account: credential?.account || '', settings, busy, error, progress, pendingSelf: !!pendingSelf, platform: current, version: app.getVersion(), secureStorage, brokerReady: !!credential?.broker };
}
function publish() { if (window && !window.isDestroyed()) window.webContents.send('store:state', snapshot()); }
async function persist() { await atomicJSON(statePath, { installed, settings, pendingSelf, catalog }); }
async function api(route, options = {}) {
  const headers = { ...options.headers };
  const expected = credential;
  if (expected) headers.Authorization = `Bearer ${expected.token}`;
  const response = await net.fetch(STORE_URL + route, { ...options, headers, redirect: 'error' });
  if (response.status === 401 && expected && credential?.token === expected.token) { brokerServer?.cancelPending(); await vault.invalidate().cleared; }
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.error || `Store request failed (${response.status}).`);
  }
  return response;
}
async function refresh(runUpdates = false) {
  if (checking) return;
  checking = true; error = ''; publish();
  try {
    const route = credential ? '/v1/apps' : '/v1/desktop/apps';
    const incoming = (await (await api(`${route}?platform=${current.platform}&arch=${current.arch}`)).json()).apps;
    catalog = incoming.concat(catalog.filter(a => installed[a.id] && !incoming.some(next => next.id === a.id)).map(a => ({ ...a, unlisted: true })));
    await persist();
    // Only this running binary's source version determines whether the store
    // needs an update. A staged new binary is not yet a running update.
    if (pendingSelf && pendingSelf.versionCode <= version.versionCode) { pendingSelf = null; await persist(); }
  } catch (e) { error = e.message; }
  finally { checking = false; publish(); }
  if (runUpdates && settings.autoUpdate && credential && !busy) await updateAll();
}
async function login(username) {
  if (typeof username !== 'string' || !username.trim() || username.length > 256) throw new Error('Enter your Gravitas username.');
  if (loginWindow) { loginWindow.focus(); return; }
  const generation = ++authGeneration;
  const brokerRequested = brokerLoginFor(username);
  const state = randomBytes(32).toString('base64url'), verifier = randomBytes(32).toString('base64url');
  const challenge = createHash('sha256').update(verifier).digest('base64url');
  const response = await net.fetch(STORE_URL + '/v1/auth/gravitas/start', { method: 'POST', redirect: 'error', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ state, codeChallenge: challenge, username: username.trim(), broker: brokerRequested }), signal: AbortSignal.timeout(20000) });
  const start = await response.json();
  if (!response.ok || start.redirectUri !== STORE_URL + '/auth/android/callback' || new URL(start.authorizationUrl).protocol !== 'https:') throw new Error('Could not start Gravitas sign-in.');
  if (generation !== authGeneration) return;
  const authSession = session.fromPartition('login-' + randomUUID());
  authSession.setPermissionRequestHandler((_wc, _permission, answer) => answer(false));
  const browser = new BrowserWindow({ title: 'Sign in with Gravitas', width: 620, height: 760, parent: window, webPreferences: { session: authSession, nodeIntegration: false, contextIsolation: true, sandbox: true } });
  loginWindow = browser;
  browser.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  browser.webContents.on('will-navigate', (event, url) => { if (!url.startsWith('https://')) event.preventDefault(); });
  let exchanging = false, completed = false;
  const finish = async (event, url) => {
    const target = new URL(url);
    if (target.origin + target.pathname !== start.redirectUri) return;
    event?.preventDefault?.();
    const q = target.searchParams;
    if (exchanging || generation !== authGeneration || q.getAll('state').length !== 1 || q.get('state') !== state || q.getAll('code').length !== 1 || q.has('error')) return;
    exchanging = true;
    try {
      const exchange = await net.fetch(STORE_URL + '/v1/auth/gravitas/exchange', { method: 'POST', redirect: 'error', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: q.get('code'), codeVerifier: verifier, username: username.trim(), deviceName: `${os.hostname()} (${current.platform})`.slice(0, 120), broker: brokerRequested }), signal: AbortSignal.timeout(20000) });
      const data = await exchange.json();
      if (!exchange.ok || typeof data.token !== 'string' || !/^[a-f0-9]{64}$/.test(data.token)) throw new Error('Sign-in could not be completed.');
      if (brokerRequested) validateSession(data.broker);
      if (generation !== authGeneration) return;
      if (!await replaceCredential(credential, data, () => generation === authGeneration)) return;
      completed = true;
      browser.close();
      await refresh(true);
    } catch (e) { error = e.message; publish(); exchanging = false; }
  };
  browser.webContents.on('will-redirect', finish);
  browser.webContents.on('did-navigate', finish);
  browser.on('closed', () => {
    if (!completed && generation === authGeneration) authGeneration++;
    if (loginWindow === browser) loginWindow = null;
    authSession.clearStorageData().catch(() => {});
  });
  await browser.loadURL(start.authorizationUrl);
}
async function install(id) {
  if (!credential) throw new Error('Sign in with Gravitas to install apps.');
  if (busy) throw new Error('Another installation is in progress.');
  const selected = catalog.find(a => a.id === id);
  if (!selected) throw new Error('App not found for this computer.');
  const artifact = artifactFor(selected, current);
  const previous = installed[id];
  if ((id === 'appstore' ? version.versionCode : previous?.versionCode || 0) >= selected.desktopRelease.versionCode) throw new Error('This app is already up to date.');
  if (previous?.versionCode === selected.desktopRelease.versionCode && id === 'appstore') { pendingSelf = previous; publish(); return; }
  busy = true; error = ''; progress = { id, fraction: 0, phase: 'Downloading' }; publish();
  const directory = await fs.mkdtemp(path.join(app.getPath('temp'), 'private-store-'));
  try {
    const download = path.join(directory, artifact.file);
    const response = await api(releasePath(selected, current), { signal: AbortSignal.timeout(20 * 60 * 1000) });
    let lastPublish = 0;
    await downloadVerified(response, download, artifact, fraction => {
      progress = { id, fraction, phase: 'Downloading' };
      if (Date.now() - lastPublish > 150) { publish(); lastPublish = Date.now(); }
    });
    progress = { id, fraction: 1, phase: 'Installing' }; publish();
    const next = await installPackage({ app: selected, archive: download, root, current });
    await createShortcut({ app: selected, installed: next, home: os.homedir() });
    installed[id] = next;
    if (id === 'appstore') pendingSelf = next;
    await persist();
  } finally { await fs.rm(directory, { recursive: true, force: true }); busy = false; progress = null; publish(); }
}
async function updateAll() {
  for (const selected of catalog) {
    const local = selected.id === 'appstore' ? { versionCode: version.versionCode } : installed[selected.id];
    if (!updateAvailable(selected, local) || (selected.id === 'appstore' && pendingSelf?.versionCode >= selected.desktopRelease.versionCode)) continue;
    try { await install(selected.id); } catch (e) { error = e.message; publish(); break; }
  }
}
async function launch(id) {
  if (!validID(id) || !installed[id]) throw new Error('Install this app first.');
  const local = installed[id];
  if (local.platform === 'macos') {
    const result = await shell.openPath(local.entry); if (result) throw new Error(result);
  } else {
    await new Promise((resolve, reject) => {
      const child = spawn(local.entry, [], { detached: true, stdio: 'ignore', cwd: path.dirname(local.entry) });
      child.once('error', reject); child.once('spawn', () => { child.unref(); resolve(); });
    });
  }
}
async function restart() {
  if (!pendingSelf) throw new Error('No store update is ready.');
  const execPath = await executable(pendingSelf);
  await fs.access(execPath);
  app.relaunch({ execPath, args: [] }); app.quit();
}
const actions = {
  state: () => snapshot(),
  refresh: () => refresh(false),
  login,
  install,
  launch,
  updateAll,
  restart,
  settings: async value => { if (typeof value !== 'boolean') throw new Error('Invalid update preference.'); settings.autoUpdate = value; await persist(); publish(); },
  logout: async () => {
    authGeneration++;
    loginWindow?.close();
    // Local sign-out takes effect before any network or filesystem await.
    const { previous, cleared } = vault.invalidate();
    brokerServer?.cancelPending();
    await cleared;
    try {
      if (previous) {
        const response = await net.fetch(STORE_URL + '/v1/device', { method: 'DELETE', redirect: 'error', headers: { Authorization: 'Bearer ' + previous.token }, signal: AbortSignal.timeout(20000) });
        if (!response.ok && response.status !== 401) throw new Error('Signed out on this computer. The Store could not revoke its old device enrollment.');
      }
    } finally { await refresh(); }
  },
};
app.whenReady().then(async () => {
  const directory = app.getPath('userData');
  await fs.mkdir(directory, { recursive: true, mode: 0o700 });
  statePath = path.join(directory, 'state.json'); sessionPath = path.join(directory, 'session.enc');
  root = path.join(directory, 'apps');
  const state = await readJSON(statePath, { installed: {}, settings, pendingSelf: null });
  installed = state.installed; settings = state.settings; pendingSelf = state.pendingSelf; catalog = state.catalog || [];
  secureStorage = await safeStorage.isAsyncEncryptionAvailable();
  // Electron's Linux fallback is not secure credential storage.
  if (process.platform === 'linux') secureStorage = secureStorage && safeStorage.isEncryptionAvailable() && ['gnome_libsecret', 'kwallet', 'kwallet5', 'kwallet6'].includes(safeStorage.getSelectedStorageBackend());
  if (secureStorage) {
    try { credential = await credentials.loadCredential(sessionPath, safeStorage); }
    catch (e) { if (e.code !== 'ENOENT') error = 'Saved sign-in could not be unlocked. Sign in again.'; }
  }
  protocol.handle('store', request => {
    const url = new URL(request.url);
    const relative = decodeURIComponent(url.pathname).replace(/^\//, '') || 'index.html';
    const file = path.resolve(__dirname, 'ui', relative);
    if (url.host !== 'app' || !file.startsWith(path.join(__dirname, 'ui') + path.sep)) return new Response('Not found', { status: 404 });
    return net.fetch(pathToFileURL(file).href);
  });
  ipcMain.handle('store:action', async (event, name, value) => {
    if (quitting) throw new Error('Private Store is quitting.');
    if (event.sender !== window?.webContents || event.senderFrame !== window.webContents.mainFrame || event.senderFrame.url !== 'store://app/index.html' || !Object.hasOwn(actions, name)) throw new Error('Invalid store action.');
    try { return await actions[name](value); } catch (e) { error = e.message; publish(); throw e; }
  });
  function createWindow() {
    window = new BrowserWindow({ title: 'Private Store', width: 1180, height: 820, minWidth: 760, minHeight: 600, backgroundColor: '#f5f4ee', webPreferences: { preload: path.join(__dirname, 'preload.cjs'), contextIsolation: true, sandbox: true, nodeIntegration: false } });
    window.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
    window.webContents.on('will-navigate', event => event.preventDefault());
    window.webContents.session.setPermissionRequestHandler((_wc, _permission, answer) => answer(false));
    window.loadURL('store://app/index.html');
    window.webContents.once('did-finish-load', () => refresh(true));
  }
  Menu.setApplicationMenu(Menu.buildFromTemplate([{ label: 'Private Store', submenu: [{ role: 'about' }, { role: 'quit' }] }, { role: 'editMenu' }, { role: 'windowMenu' }]));
  createWindow();
  brokerEngine = new BrokerEngine({ fetch: (...args) => net.fetch(...args), getCredential: () => credential, replaceCredential, approve: async ({ appOrigin, account, signal }) => {
    if (!window || window.isDestroyed()) createWindow();
    window.show(); window.focus();
    const result = await dialog.showMessageBox(window, { signal, type: 'question', title: 'Sign in with Private Store', message: `Sign in to ${new URL(appOrigin).host}?`, detail: `Use ${account} from Private Store for ${appOrigin}.\n\nOnly your basic profile is shared. Approve only if you just asked an app to sign in to this service.`, buttons: ['Cancel', 'Sign in'], defaultId: 0, cancelId: 0, noLink: true });
    return result.response === 1;
  } });
  const launch = { file: process.env.APPIMAGE || process.execPath, args: app.isPackaged ? [] : [__dirname] };
  brokerServer = await serveBroker({ launch, authorize: (request, signal) => brokerEngine.authorize(request, signal) });
  app.on('activate', () => { if (!BrowserWindow.getAllWindows().length) createWindow(); });
  setInterval(() => refresh(true).catch(e => { error = e.message; publish(); }), 6 * 60 * 60 * 1000).unref();
}).catch(e => { console.error(e); app.quit(); });
let quitReady = false, quitting = false;
app.on('before-quit', event => {
  if (quitReady) return;
  event.preventDefault();
  if (quitting) return;
  quitting = true; authGeneration++;
  brokerServer?.cancelPending(); brokerServer?.server.closeAllConnections(); brokerServer?.server.close();
  Promise.resolve(brokerEngine?.queue).catch(() => {}).then(() => vault.settled()).catch(() => {}).finally(() => { quitReady = true; app.quit(); });
});
app.on('second-instance', () => { window?.show(); window?.focus(); });
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit(); });
