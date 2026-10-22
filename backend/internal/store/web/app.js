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
