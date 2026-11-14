const feedback = document.querySelector('#feedback');
document.querySelector('#logout')?.addEventListener('click', async (event) => {
  const button = event.currentTarget;
  button.disabled = true;
  try {
    const response = await fetch('/auth/gravitas/logout', { method: 'POST', headers: { 'X-Gravitas-CSRF': '1' } });
    if (!response.ok) throw new Error();
    window.location.replace('/');
  } catch {
    feedback.textContent = 'Couldn’t sign out. Please try again.';
    button.disabled = false;
  }
});
document.querySelector('#copy')?.addEventListener('click', async () => {
  try {
    await navigator.clipboard.writeText(document.querySelector('#server-url').textContent);
    feedback.textContent = 'Store address copied.';
  } catch {
    feedback.textContent = 'Select and copy the store address above.';
  }
});
window.addEventListener('pageshow', (event) => { if (event.persisted) window.location.reload(); });

// The page stays public, but an open signed-in view must follow central logout.
if (document.querySelector('#logout')) {
  const notice = document.querySelector('#session-feedback');
  const links = [...document.querySelectorAll('a[href="/download/appstore.apk"], a[href^="/download/desktop/"]')];
  let checking = false;
  function unavailable(value) {
    if (notice) { notice.hidden = !value; notice.textContent = value ? 'Gravitas is temporarily unavailable. Please try again shortly.' : ''; }
    document.querySelectorAll('.session').forEach(node => { node.hidden = value; });
    links.forEach(link => { if (value) { link.setAttribute('aria-disabled', 'true'); link.setAttribute('tabindex', '-1'); } else { link.removeAttribute('aria-disabled'); link.removeAttribute('tabindex'); } });
  }
  links.forEach(link => link.addEventListener('click', event => { if (link.getAttribute('aria-disabled') === 'true') event.preventDefault(); }));
  async function checkSession() {
    if (document.hidden || checking) return;
    checking = true;
    try {
      const result = await fetch('/auth/gravitas/session', { credentials: 'same-origin', cache: 'no-store' });
      if (result.status === 401) { window.location.reload(); return; }
      unavailable(!result.ok);
    } catch { unavailable(true); } finally { checking = false; }
  }
  window.addEventListener('focus', checkSession);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) checkSession(); });
  window.setInterval(checkSession, 15000);
  checkSession();
}

document.querySelector('#catalog-global')?.addEventListener('click', async (event) => {
  const button = event.currentTarget;
  const status = document.querySelector('#catalog-global-status');
  const global = button.dataset.global !== 'true';
  button.disabled = true;
  try {
    const response = await fetch('/admin/catalog/' + encodeURIComponent(button.dataset.app) + '/visibility', {
      method: 'POST',
      headers: { 'X-Gravitas-CSRF': '1', 'Content-Type': 'application/json' },
      body: JSON.stringify({ global }),
    });
    if (!response.ok) throw new Error();
    button.dataset.global = String(global);
    button.textContent = global ? 'Remove global visibility' : 'Show to all Store users';
    status.textContent = global ? 'Global visibility enabled.' : 'Global visibility removed.';
  } catch {
    status.textContent = 'Couldn’t change visibility. Please try again.';
  } finally {
    button.disabled = false;
  }
});
