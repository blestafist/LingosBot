(() => {
  const systemTheme = window.matchMedia('(prefers-color-scheme: dark)');
  let choice, button;
  try { choice = localStorage.getItem('lingos-theme'); } catch { /* Storage can be disabled. */ }
  if (choice !== 'light' && choice !== 'dark') choice = null;
  function apply() {
    const theme = choice || (systemTheme.matches ? 'dark' : 'light');
    document.documentElement.dataset.theme = theme;
    if (button) {
      const label = `Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`;
      button.setAttribute('aria-label', label);
      button.setAttribute('title', label);
    }
  }
  apply();
  systemTheme.addEventListener('change', apply);
  document.addEventListener('DOMContentLoaded', () => {
    button = document.getElementById('theme-toggle');
    if (!button) return;
    apply();
    button.addEventListener('click', () => {
      choice = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
      apply();
      try { localStorage.setItem('lingos-theme', choice); } catch { /* Keep the choice for this page. */ }
    });
  });
})();
