'use strict';
let state = { apps: [], installed: {}, settings: {}, platform: {}, busy: false }, view = 'catalog', selectedID = null;
const $ = selector => document.querySelector(selector);
const text = (tag, content, className) => { const node = document.createElement(tag); node.textContent = content; if (className) node.className = className; return node; };
function button(label, action, className) { const node = text('button', label, className); node.onclick = () => action(); return node; }
async function act(name, value) { try { await window.store.action(name, value); } catch (e) { $('#error').hidden = false; $('#error').textContent = e.message.replace(/^Error invoking remote method '[^']+': Error: /, ''); } }
const size = bytes => bytes >= 1e6 ? `${(bytes / 1e6).toFixed(1)} MB` : `${Math.ceil(bytes / 1000)} kB`;
const updates = () => state.apps.filter(app => state.installed[app.id] && app.desktopRelease.versionCode > state.installed[app.id].versionCode);
function icon(app) { return text('span', app.id === 'appstore' ? 'p↗' : app.name.charAt(0), `icon ${app.id}`); }
function installButton(app) {
  const local = state.installed[app.id], needsUpdate = local && app.desktopRelease.versionCode > local.versionCode;
  let node;
  if (app.id === 'appstore' && state.pendingSelf) node = button('Restart to update ↗', () => act('restart'), 'download');
  else if (local && !needsUpdate) node = app.id === 'appstore' ? text('span', 'Up to date', 'meta') : button('Open ↗', () => act('launch', app.id));
  else node = button(needsUpdate ? 'Update ↓' : 'Install ↓', () => act('install', app.id), 'download');
  if (node.tagName === 'BUTTON') node.disabled = state.busy || (!state.account && (!local || needsUpdate));
  return node;
}
function card(app) {
  const node = text('article', '', 'card'), top = text('div', '', 'card-top'), identity = text('div', '');
  identity.append(text('h2', app.name), text('div', `v${app.desktopRelease.version} · ${size(app.desktopRelease.artifacts[0].size)}`, 'meta'));
  top.append(icon(app), identity); node.append(top, text('p', app.description));
  if (state.progress?.id === app.id) { const progress = document.createElement('progress'); progress.max = 1; progress.value = state.progress.fraction; node.append(text('span', `${state.progress.phase} · ${Math.round(state.progress.fraction * 100)}%`, 'progress-label'), progress); }
  const actions = text('div', '', 'card-actions');
  actions.append(button('App details ↗', () => { selectedID = app.id; render(); }, 'text-button'), installButton(app)); node.append(actions); return node;
}
function detail(app) {
  const node = text('article', '', 'detail');
  node.append(button('← Back to the collection', () => { selectedID = null; render(); }, 'text-button'));
  const identity = text('div', '', 'identity'); identity.append(icon(app), text('h2', app.name)); node.append(identity, text('p', app.description), installButton(app));
  const facts = text('div', '', 'facts'); facts.append(text('span', `${state.platform.platform} · ${state.platform.arch}`), text('span', `v${app.desktopRelease.version}`), text('span', size(app.desktopRelease.artifacts[0].size))); node.append(facts, text('h3', 'What’s new'));
  const changes = document.createElement('ul'); app.desktopRelease.changes.forEach(change => changes.append(text('li', change))); node.append(changes);
  node.append(text('p', `From ${app.repository}. Downloads are verified against the release checksum before installation. Apps install for your user; updating an open app takes effect the next time you launch it from Private Store or its shortcut.`));
  return node;
}
function render() {
  $('#platform').textContent = `${({ macos: 'macOS', windows: 'Windows', linux: 'Linux' })[state.platform.platform] || 'Desktop'} · ${state.platform.arch || ''}`;
  $('#version').textContent = `Private Store ${state.version || ''}`;
  $('#error').hidden = !state.error; $('#error').textContent = state.error || '';
  $('#restart').hidden = !state.pendingSelf;
  $('#update-count').textContent = updates().length || '';
  const account = $('#account');
  // Preserve a typed username and keyboard focus during background refreshes.
  if (account.dataset.account !== (state.account || 'guest')) {
    account.dataset.account = state.account || 'guest'; account.replaceChildren();
    if (state.account) account.append(text('span', '', 'dot'), text('span', state.account));
    else {
      const form = document.createElement('form'), input = document.createElement('input');
      input.name = 'username'; input.placeholder = 'username@gravitas.cool'; input.setAttribute('aria-label', 'Gravitas username'); input.autocomplete = 'username'; input.maxLength = 256; input.required = true;
      const submit = text('button', 'Sign in 🔥'); submit.type = 'submit';
      form.append(input, submit); form.onsubmit = event => { event.preventDefault(); act('login', input.value); }; account.append(form);
    }
  }
  document.querySelectorAll('[data-view]').forEach(node => node.classList.toggle('active', node.dataset.view === view));
  const settings = view === 'settings';
  $('#heading').hidden = !!selectedID || settings; $('#toolbar').hidden = !!selectedID || settings;
  const content = $('#content'); content.replaceChildren();
  if (settings) {
    content.append(text('p', 'YOUR STORE, YOUR WAY', 'eyebrow'), text('h1', 'Settings.'));
    const row = text('div', '', 'setting'), copy = text('div', '');
    copy.append(text('h3', 'Keep installed apps current'), text('p', 'Check at launch and every six hours while Private Store is open. Download updates for installed apps, including the store. Restart the store when its update is ready.'));
    const toggle = document.createElement('input'); toggle.type = 'checkbox'; toggle.checked = !!state.settings.autoUpdate; toggle.setAttribute('aria-label', 'Automatic app updates'); toggle.onchange = () => act('settings', toggle.checked); row.append(copy, toggle); content.append(row);
    const session = text('div', '', 'setting'); session.append(text('p', state.secureStorage ? 'Your sign-in is encrypted using your operating system’s credential storage.' : 'Secure credential storage is unavailable. Sign-in lasts for this session; no credential is saved to disk.'));
    if (state.account) session.append(button('Sign out ↗', () => act('logout'))); content.append(session);
    content.append(text('p', 'Apps use separate versions on Android and desktop. This store shows only published builds compatible with this computer. Desktop notifications are not required.', 'lede')); return;
  }
  if (selectedID) { const selected = state.apps.find(app => app.id === selectedID); if (selected) { content.append(detail(selected)); return; } selectedID = null; }
  const search = $('#search').value.toLowerCase();
  let apps = state.apps.filter(app => (view !== 'catalog' || !app.unlisted) && `${app.name} ${app.description}`.toLowerCase().includes(search));
  if (view === 'installed') apps = apps.filter(app => state.installed[app.id]);
  if (view === 'updates') apps = apps.filter(app => updates().includes(app));
  $('#app-count').textContent = `${apps.length} app${apps.length === 1 ? '' : 's'}`;
  $('#update-all').hidden = !state.account || !updates().length; $('#update-all').disabled = state.busy;
  if (!apps.length) {
    const empty = text('div', '', 'empty'); empty.append(text('h2', view === 'updates' ? 'Everything in its place.' : view === 'installed' ? 'Your collection starts here.' : 'A little room to grow.'), text('p', view === 'updates' ? 'Your installed apps are up to date.' : view === 'installed' ? 'Install an app from Discover to find it here.' : 'No compatible published apps match this view. Try another search or check again soon.')); content.append(empty);
  } else { const grid = text('div', '', 'grid'); apps.forEach(app => grid.append(card(app))); content.append(grid); }
}
document.querySelectorAll('[data-view]').forEach(node => { node.onclick = () => { view = node.dataset.view; selectedID = null; render(); }; });
document.querySelector('.brand').onclick = event => { event.preventDefault(); view = 'catalog'; selectedID = null; render(); };
$('#search').oninput = render; $('#refresh').onclick = () => act('refresh'); $('#update-all').onclick = () => act('updateAll'); $('#restart-button').onclick = () => act('restart');
window.store.subscribe(next => { state = next; render(); });
window.store.action('state').then(next => { state = next; render(); });
