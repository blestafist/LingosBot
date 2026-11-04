(() => {
  const choices = ['system', 'light', 'dark'];
  let choice = 'system';
  try { choice = localStorage.getItem('lingos-theme') || 'system'; } catch { /* Storage can be disabled. */ }
  if (!choices.includes(choice)) choice = 'system';
  function apply() {
    document.documentElement.dataset.theme = choice;
  }
  apply();
  document.addEventListener('DOMContentLoaded', () => {
    const select = document.getElementById('theme-choice');
    select.value = choice;
    select.addEventListener('change', () => {
      choice = choices.includes(select.value) ? select.value : 'system';
      apply();
      try { localStorage.setItem('lingos-theme', choice); } catch { /* Keep the choice for this page. */ }
    });
  });
})();
