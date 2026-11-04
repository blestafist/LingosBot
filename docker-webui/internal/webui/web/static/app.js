import { settingLimits, settingsPayload, settingsSignature, schedulePayload, normalizeSchedule, scheduleSummary, httpDescription, eventDescription, dateLabel, exportURL, passwordError, filterLibrary, selectTopics } from './helpers.js';

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}
function button(text, handler, className = 'button') {
  const node = element('button', className, text);
  node.type = 'button';
  node.addEventListener('click', handler);
  return node;
}
function revealSecret(control) {
  const input = document.getElementById(control.dataset.reveal);
  if (!input) return;
  const reveal = input.type === 'password';
  input.type = reveal ? 'text' : 'password';
  control.textContent = reveal ? 'Hide' : 'Show';
  control.setAttribute('aria-pressed', String(reveal));
}
document.addEventListener('click', event => {
  const control = event.target.closest('[data-reveal]');
  if (control) revealSecret(control);
});
$$('input[type="password"]').forEach(input => { input.maxLength = 1024; });
document.addEventListener('input', event => {
  if (event.target instanceof HTMLInputElement) event.target.setCustomValidity('');
});
document.addEventListener('invalid', event => {
  let details = event.target.closest('details');
  while (details) { details.open = true; details = details.parentElement.closest('details'); }
}, true);
document.addEventListener('submit', event => {
  for (const input of $$('input[autocomplete="current-password"], input[autocomplete="new-password"]', event.target)) {
    input.setCustomValidity(passwordError(input.value, input.minLength === 12));
    if (!input.validity.valid) {
      event.preventDefault(); event.stopImmediatePropagation();
      input.reportValidity();
      return;
    }
  }
}, true);

$('#login-form')?.addEventListener('submit', async event => {
  event.preventDefault();
  const form = event.currentTarget;
  const body = new URLSearchParams(new FormData(form));
  const controls = $$('button, input', form);
  const submit = $('button[type="submit"]', form);
  const errorNode = $('#login-error');
  errorNode.hidden = true;
  controls.forEach(control => { control.disabled = true; });
  submit.textContent = 'Signing in…';
  form.setAttribute('aria-busy', 'true');
  try {
    let response;
    try { response = await fetch('/login', { method: 'POST', credentials: 'same-origin', redirect: 'follow', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body }); }
    catch { throw new Error('Could not reach the server. Check your connection and try again.'); }
    if (response.ok && response.redirected) { location.assign('/'); return; }
    const text = await response.text();
    let detail;
    try { detail = JSON.parse(text).error; } catch { detail = !/^\s*</.test(text) ? text.trim().slice(0, 500) : ''; }
    if (response.status === 401) detail = 'Check your email or administrator login and password, then try again.';
    else if (response.status === 429) detail = 'Too many sign-in attempts. Wait a few minutes and try again.';
    throw new Error(detail || 'Sign-in could not be completed. Please try again.');
  } catch (error) {
    errorNode.textContent = error.message;
    errorNode.hidden = false;
  } finally {
    controls.forEach(control => { control.disabled = false; });
    submit.textContent = 'Sign in';
    form.removeAttribute('aria-busy');
  }
});

const csrf = $('#csrf-token')?.value;
if (csrf) initWorkspace();

function initWorkspace() {
  const state = {
    settings: null, settingsBusy: false, settingsLoading: false, settingsRevision: 0,
    classes: [], classesLoaded: false, credentials: null, runs: [], library: [],
    selectedRun: null, activeRun: null, cursor: 0, pollingGeneration: 0,
    pollTimer: null, watchTimer: null, httpCount: 0, logTrimmed: false,
    progress: { lessons: 0, words: 0, lessonWords: 0 }, starting: false,
    schedule: null, scheduleLoaded: false, scheduleDirty: false,
    selectedTopics: new Set(), downloading: false,
  };
  let account = null;
  let overrideID = 0;
  let watchingRuns = false;
  const settingsForm = $('#settings-form');
  const runLabels = { run: 'Lessons', classes: 'Class sync', scrape: 'Word collection' };
  const statusLabels = { running: 'Running', completed: 'Completed', failed: 'Failed', cancelled: 'Stopped', interrupted: 'Interrupted' };

  async function api(path, { method = 'GET', body, signal, download = false } = {}) {
    let response;
    try {
      response = await fetch(path, {
        method, credentials: 'same-origin', signal,
        headers: { Accept: 'application/json', ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}), ...(method !== 'GET' && method !== 'HEAD' ? { 'X-CSRF-Token': csrf } : {}) },
        ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
      });
    } catch (error) {
      if (error.name === 'AbortError') throw error;
      throw new Error('Could not reach the server. Check your connection and try again.');
    }
    if (response.ok && download) {
      const expected = body.format === 'md' ? 'text/markdown' : 'application/json';
      if (response.headers.get('Content-Type')?.split(';')[0].trim() !== expected) throw new Error('The server did not return a vocabulary file. Check that you are still signed in and try again.');
      return response.blob();
    }
    const text = await response.text();
    let data;
    try { data = text ? JSON.parse(text) : null; } catch { data = null; }
    if (!response.ok) {
      const fallback = { 400: 'Check the form and try again.', 401: 'Your session has expired. Sign in again.', 403: 'This action is not allowed. Refresh the page or contact your administrator.', 404: 'This item is no longer available.', 409: 'The worker is busy or this item changed. Refresh and try again.', 429: 'Too many requests. Please wait a moment before trying again.', 500: 'The server could not complete the request. Please try again.' };
      const error = typeof data?.error === 'string' ? data.error : text.trim() && !/^\s*</.test(text) ? text.trim().slice(0, 500) : fallback[response.status] || `Request failed (status ${response.status}). Try again.`;
      throw new Error(error === 'invalid password' ? 'Your current password is incorrect.' : error === 'unauthorized' ? fallback[401] : error === 'forbidden' ? fallback[403] : error);
    }
    if (text && data === null) throw new Error('The server returned an unreadable response. Refresh and try again.');
    return data;
  }

  function message(target, text, error = true) {
    const node = typeof target === 'string' ? $(target) : target;
    node.textContent = text || '';
    node.hidden = !text;
    node.classList.toggle('error', error);
  }
  function notify(text, error = false) {
    const notice = $('#notice');
    notice.setAttribute('role', error ? 'alert' : 'status');
    message(notice, text, error);
    if (error) notice.focus({ preventScroll: true });
  }
  function empty(container, title, description, retry) {
    container.replaceChildren();
    const block = element('div', 'empty');
    block.append(element('strong', '', title));
    if (description) block.append(element('p', '', description));
    if (retry) block.append(button('Try again', retry));
    container.append(block);
  }
  function status(node, text, kind = '') {
    node.textContent = text;
    node.className = `status ${kind}`;
  }
  async function confirmAction(title, description, accept = 'Confirm') {
    const dialog = $('#confirm-dialog');
    $('#confirm-title').textContent = title;
    $('#confirm-description').textContent = description;
    $('#confirm-accept').textContent = accept;
    dialog.returnValue = 'cancel';
    dialog.showModal();
    return new Promise(resolve => dialog.addEventListener('close', () => resolve(dialog.returnValue === 'confirm'), { once: true }));
  }
  async function withForm(form, errorTarget, task) {
    message(errorTarget, '');
    const controls = $$('button, input', form);
    const previous = controls.map(control => control.disabled);
    controls.forEach(control => { control.disabled = true; });
    form.setAttribute('aria-busy', 'true');
    try { await task(); }
    catch (error) { message(errorTarget, error.message); }
    finally {
      controls.forEach((control, index) => { control.disabled = previous[index]; });
      form.removeAttribute('aria-busy');
    }
  }
  function resetSecrets(form) {
    $$('[data-reveal]', form).forEach(control => {
      const input = document.getElementById(control.dataset.reveal);
      input.value = '';
      input.type = 'password';
      control.textContent = 'Show';
      control.setAttribute('aria-pressed', 'false');
    });
  }
  $('form[action="/logout"]').addEventListener('submit', async event => {
    event.preventDefault();
    const control = $('button', event.currentTarget);
    control.disabled = true;
    try {
      const response = await fetch('/logout', { method: 'POST', credentials: 'same-origin', redirect: 'follow', headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-CSRF-Token': csrf }, body: new URLSearchParams({ csrf }) });
      if (!response.ok) throw new Error('Could not sign out. Refresh the page and try again.');
      location.assign('/');
    } catch (error) { notify(error.message || 'Could not reach the server. Try signing out again.', true); control.disabled = false; }
  });

  function showPage(focus = false) {
    const id = location.hash.slice(1).split('?')[0];
    const page = $$('[data-page]').find(item => item.id === (id === 'collect-words' ? 'library' : id)) || $('#lessons');
    $$('[data-page]').forEach(item => { item.hidden = item !== page; });
    $$('[data-page-link]').forEach(link => {
      if (link.dataset.pageLink === page.id) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    });
    const heading = $('h1', page);
    $('#page-context').textContent = heading.textContent;
    document.title = `${heading.textContent} · Your workspace`;
    if (focus) {
      const target = id === 'collect-words' ? $('#collect-words h2') : heading;
      target.tabIndex = -1; target.focus({ preventScroll: true });
    }
    if (id === 'collect-words') $('#collect-words').scrollIntoView();
    else if (focus) window.scrollTo(0, 0);
    if (page.id === 'schedule' && state.scheduleLoaded && !state.scheduleDirty) loadSchedule(false);
  }
  window.addEventListener('hashchange', () => showPage(true));
  showPage();

  function overrideRows() {
    return $$('.override-row').map(row => ({ title: row.dataset.title ?? $('.override-title', row).value, count: $('.override-count', row).value }));
  }
  function currentSettings() {
    const values = Object.fromEntries(Object.keys(settingLimits).map(key => [key, settingsForm.elements.namedItem(key).value]));
    return settingsPayload(values, overrideRows());
  }
  function settingsDirty() {
    if (!state.settings) return false;
    try { return settingsSignature(currentSettings()) !== settingsSignature(state.settings); }
    catch { return true; }
  }
  function updateRunControls() {
    const busy = state.starting || Boolean(state.activeRun);
    $$('[data-start], [data-run-control]').forEach(control => { control.disabled = busy; });
    $('#run-lessons').disabled = !state.settings || state.settingsBusy || busy || !state.credentials?.saved;
    $('#save-settings').disabled = !state.settings || state.settingsBusy;
    const dirty = settingsDirty();
    $('#class-plan-count').textContent = state.classesLoaded ? `${state.classes.length} classes` : '';
    $('#run-lessons').textContent = state.settingsBusy ? 'Saving plan…' : state.starting ? 'Starting…' : dirty ? 'Save & run lessons →' : 'Run lessons →';
    if (state.settings) {
      status($('#settings-state'), state.settingsBusy ? 'Saving…' : dirty ? 'Unsaved changes' : 'Saved', dirty ? '' : 'good');
      $('#settings-hint').textContent = !state.credentials ? 'Check your account connection before running.' : !state.credentials.saved ? 'Connect Lingos in Account before running lessons.' : dirty ? 'Running saves and uses this plan first.' : 'Manual and scheduled runs use your saved plan.';
    }
    $('#library-run-status').textContent = state.activeRun ? `${runLabels[state.activeRun.mode] || 'Worker'} is running. Follow progress in Lessons.` : '';
    $('#library-run-status').hidden = !state.activeRun;
  }
  function addOverride(title = '', value = '', manual = false) {
    const row = element('div', `override-row${manual ? ' manual' : ''}`);
    const unique = `override-${++overrideID}`;
    const count = element('input', 'override-count');
    count.id = unique;
    count.type = 'number'; count.min = '0'; count.max = '10000'; count.step = '1';
    count.placeholder = 'Default'; count.value = value;
    if (manual) {
      const titleLabel = element('label', '', 'Class title');
      const input = element('input', 'override-title');
      input.value = title; input.placeholder = 'Exact class title';
      input.setAttribute('aria-label', 'Manual class title');
      titleLabel.append(input);
      const countLabel = element('label', '', 'Lessons');
      countLabel.append(count);
      const remove = button('×', () => { row.remove(); state.settingsRevision++; updateRunControls(); }, 'button quiet');
      remove.setAttribute('aria-label', `Remove ${title || 'manual'} class override`);
      row.append(titleLabel, countLabel, remove);
    } else {
      row.dataset.title = title;
      const label = element('label', '', title);
      label.htmlFor = unique;
      row.append(label, count);
    }
    count.setAttribute('aria-label', `Lessons for ${title || 'manual class'}`);
    $('#class-counts').append(row);
    return row;
  }
  function renderOverrides(overrides) {
    const container = $('#class-counts');
    container.replaceChildren();
    const remaining = new Map(Object.entries(overrides));
    const seen = new Set();
    for (const item of state.classes) {
      const folded = item.title.normalize('NFKC').toLocaleLowerCase('und');
      if (seen.has(folded)) continue;
      seen.add(folded);
      const matching = [...remaining.keys()].find(title => title.normalize('NFKC').toLocaleLowerCase('und') === folded);
      addOverride(item.title, matching === undefined ? '' : remaining.get(matching));
      if (matching !== undefined) remaining.delete(matching);
    }
    for (const [title, count] of remaining) addOverride(title, count, true);
    if (!container.children.length) container.append(element('p', 'empty', 'No classes synced yet. Sync classes, or add an exact class title below.'));
  }
  async function loadSettings() {
    if (state.settingsLoading) return;
    state.settingsLoading = true;
    message('#settings-error', '');
    $('#retry-settings').hidden = true;
    try {
      const data = await api('/api/settings');
      if (!data?.settings) throw new Error('The saved lesson plan could not be read. Try again.');
      for (const [key, [, , fallback]] of Object.entries(settingLimits)) settingsForm.elements.namedItem(key).value = data.settings[key] ?? fallback;
      renderOverrides(data.settings.classLessonCounts || {});
      state.settings = currentSettings();
      $('#base-url').textContent = data.baseUrl || 'Not configured';
      $('#dashboard-url').textContent = data.studentDashboardUrl || 'Not configured';
      $('#execution-limit-hint').textContent = `Server limit: ${data.executionLimitSeconds} seconds. Longer runs are capped.`;
      $('#execution-limit-hint').hidden = data.executionLimitSeconds == null;
      $('#settings-fields').disabled = false;
    } catch (error) {
      message('#settings-error', error.message);
      $('#retry-settings').hidden = false;
      status($('#settings-state'), 'Unavailable', 'bad');
      $('#settings-hint').textContent = 'Load your saved plan before making changes.';
    } finally { state.settingsLoading = false; updateRunControls(); }
  }
  async function saveSettings() {
    if (!settingsForm.reportValidity()) return false;
    let payload;
    try { payload = currentSettings(); }
    catch (error) { message('#settings-error', error.message); return false; }
    state.settingsBusy = true;
    const revision = state.settingsRevision;
    $('#settings-fields').disabled = true;
    message('#settings-error', '');
    updateRunControls();
    try {
      const data = await api('/api/settings', { method: 'PUT', body: payload });
      if (!data?.settings) throw new Error('The server did not confirm your saved plan. Try again before running.');
      state.settings = settingsPayload(data.settings, Object.entries(data.settings.classLessonCounts || {}).map(([title, count]) => ({ title, count })));
      if (state.settingsRevision === revision) {
        for (const key of Object.keys(settingLimits)) settingsForm.elements.namedItem(key).value = state.settings[key];
        renderOverrides(state.settings.classLessonCounts);
      }
      return true;
    } catch (error) { message('#settings-error', error.message); return false; }
    finally { state.settingsBusy = false; $('#settings-fields').disabled = false; updateRunControls(); }
  }
  settingsForm.addEventListener('input', () => { state.settingsRevision++; updateRunControls(); });
  settingsForm.addEventListener('submit', async event => {
    event.preventDefault();
    if (!state.settingsBusy && await saveSettings()) notify('Lesson plan saved. Scheduled runs will use it too.');
  });
  $('#retry-settings').addEventListener('click', loadSettings);
  $('#add-override').addEventListener('click', () => {
    $('#class-counts > .empty')?.remove();
    const row = addOverride('', '', true);
    $('.override-title', row).focus();
  });
  $('#run-lessons').addEventListener('click', () => startRun('run'));

  function renderClassLists() {
    const container = $('#library-classes');
    container.replaceChildren();
    if (!state.classes.length) empty(container, 'No classes synced yet.', 'Save your Lingos credentials in Account, then sync classes.');
    for (const item of state.classes) {
      const row = element('div', 'class-row');
      const copy = element('div');
      copy.append(element('strong', '', item.title || 'Untitled class'), element('small', '', `Class ID ${item.group_id}`));
      const collect = button('Collect words', () => startRun('scrape', item.group_id));
      collect.dataset.runControl = '';
      collect.setAttribute('aria-label', `Collect words from ${item.title || 'class'}`);
      row.append(copy, collect);
      container.append(row);
    }
    updateRunControls();
  }
  async function loadClasses() {
    try {
      const data = await api('/api/classes');
      if (!Array.isArray(data?.classes)) throw new Error('The class list could not be read.');
      const dirty = settingsDirty();
      state.classes = data.classes;
      state.classesLoaded = true;
      renderClassLists();
      if (state.settings && !state.settingsBusy) {
        if (!dirty) renderOverrides(state.settings.classLessonCounts);
        else {
          const titles = new Set(overrideRows().map(row => row.title.trim().toLocaleLowerCase('und')));
          for (const item of state.classes) {
            if (!titles.has(item.title.toLocaleLowerCase('und'))) {
              $('#class-counts > .empty')?.remove();
              addOverride(item.title);
              titles.add(item.title.toLocaleLowerCase('und'));
            }
          }
        }
        updateRunControls();
      }
    } catch (error) {
      empty($('#library-classes'), 'Classes unavailable', error.message, loadClasses);
    }
  }
  $$('[data-start]').forEach(control => control.addEventListener('click', () => startRun(control.dataset.start)));
  $('#manual-collect').addEventListener('submit', event => {
    event.preventDefault();
    const input = $('#manual-class-id');
    if (input.reportValidity()) startRun('scrape', input.value.trim());
  });

  async function startRun(mode, classID = '') {
    if (state.starting || state.activeRun || state.settingsBusy) return;
    if (mode === 'scrape' && !/^[1-9][0-9]{0,18}$/.test(String(classID))) { notify('Enter a positive numeric class ID, up to 19 digits.', true); return; }
    state.starting = true;
    updateRunControls();
    try {
      if (mode === 'run' && !await saveSettings()) return;
      const data = await api('/api/runs', { method: 'POST', body: { mode, class_id: String(classID) } });
      if (!data?.run) throw new Error('The server did not confirm the run. Refresh history before retrying.');
      state.activeRun = data.run;
      state.runs = [data.run, ...state.runs.filter(run => String(run.id) !== String(data.run.id))];
      selectRun(data.run);
      renderHistory();
      if (mode === 'run') { const activity = $('.activity'); activity.tabIndex = -1; activity.focus(); }
      notify(mode === 'classes' ? 'Syncing your classes. Progress is saved in Lessons.' : mode === 'scrape' ? 'Collecting words into your library. You can leave this tab.' : 'Lessons started. You can leave this tab; the run continues on the server.');
    } catch (error) { notify(error.message, true); }
    finally { state.starting = false; updateRunControls(); }
  }

  function renderHistory() {
    const container = $('#run-history');
    container.replaceChildren();
    if (!state.runs.length) { empty(container, 'A fresh start.', 'Your runs will appear here, even after closing this tab.'); return; }
    for (const run of state.runs) {
      const control = button('', () => selectRun(run), 'history-item');
      control.setAttribute('aria-pressed', String(String(run.id) === String(state.selectedRun?.id)));
      const title = element('strong', '', `${runLabels[run.mode] || 'Run'}${run.source === 'schedule' ? ' · scheduled' : ''}`);
      const badge = element('span');
      status(badge, statusLabels[run.status] || run.status, run.status === 'running' ? 'running' : run.status === 'completed' ? 'good' : ['failed', 'interrupted'].includes(run.status) ? 'bad' : '');
      control.append(title, badge, element('small', '', dateLabel(run.created_at)));
      container.append(control);
    }
  }
  async function loadRuns({ initial = false, quiet = false } = {}) {
    try {
      const data = await api('/api/runs');
      if (!Array.isArray(data?.runs)) throw new Error('Run history could not be read.');
      const previousActive = state.activeRun;
      state.runs = data.runs;
      state.activeRun = state.runs.find(run => run.status === 'running') || null;
      if (initial && state.runs.length) selectRun(state.activeRun || state.runs[0]);
      else if (!state.selectedRun && state.activeRun) selectRun(state.activeRun);
      else if (state.selectedRun) {
        const selected = state.runs.find(run => String(run.id) === String(state.selectedRun.id));
        if (selected && selected.status !== state.selectedRun.status) { state.selectedRun = selected; renderRun(); }
      }
      if (!state.runs.length && !state.selectedRun) {
        status($('#run-status'), 'Ready');
        $('#run-title').textContent = 'Ready when you are.';
        $('#run-meta').textContent = 'Run lessons, sync classes or collect words.';
      }
      if (previousActive && !state.activeRun) refreshAfterRun();
      renderHistory();
      updateRunControls();
    } catch (error) {
      if (!quiet) empty($('#run-history'), 'History unavailable', error.message, () => loadRuns({ initial: !state.selectedRun }));
      if (initial) {
        status($('#run-status'), 'Unavailable', 'bad');
        $('#run-title').textContent = 'Could not restore your latest run.';
        $('#run-meta').textContent = 'Refresh history to try again. Runs continue on the server.';
      }
    }
  }
  function trimLogs() {
    const activity = $('#activity-log');
    const http = $('#http-log');
    while (activity.childElementCount + http.childElementCount > 300) {
      const firstActivity = activity.firstElementChild;
      const firstHTTP = http.firstElementChild;
      const remove = !firstActivity ? firstHTTP : !firstHTTP ? firstActivity : Number(firstActivity.dataset.seq) <= Number(firstHTTP.dataset.seq) ? firstActivity : firstHTTP;
      remove.remove();
      state.logTrimmed = true;
    }
    $('#http-summary').textContent = `HTTP requests (${state.httpCount})${state.logTrimmed ? ' · recent entries' : ''}`;
    if (state.logTrimmed) showLogNote('Showing the most recent 300 activity and HTTP entries. Progress totals remain available above.');
  }
  function showLogNote(text) {
    let note = $('#log-note');
    if (!note) { note = element('p', 'footnote'); note.id = 'log-note'; $('#run-meta').after(note); }
    note.textContent = text;
  }
  function addActivity(text, seq, bad = false) {
    const list = $('#activity-log');
    const atBottom = list.scrollHeight - list.scrollTop - list.clientHeight < 24;
    const line = element('li', bad ? 'bad' : '', text);
    line.dataset.seq = seq;
    list.append(line);
    trimLogs();
    if (atBottom) list.scrollTop = list.scrollHeight;
  }
  function handleEvent(raw, seq) {
    let event = raw;
    if (typeof event === 'string') {
      try { event = JSON.parse(event); } catch { addActivity('One saved event could not be read.', seq, true); return; }
    }
    if (!event || typeof event !== 'object') return;
    if (event.type === 'error' || event.type === 'result' && !event.success) $('.run-details').open = true;
    if (event.type === 'http_request') {
      state.httpCount++;
      const copy = httpDescription(event);
      const line = element('li');
      line.dataset.seq = seq;
      line.append(element('code', '', copy.title), element('span', '', copy.detail));
      $('#http-log').append(line);
      trimLogs();
      return;
    }
    const description = event.type === 'result' && event.success && state.selectedRun?.mode === 'classes' ? 'Classes synced.' : event.type === 'result' && event.success && state.selectedRun?.mode === 'scrape' ? 'Words collected and saved to your library.' : eventDescription(event);
    if (description) addActivity(description, seq, event.type === 'error' || event.type === 'result' && !event.success);
    if (['class_start', 'lesson_start', 'lesson_complete', 'class_complete', 'class_skipped'].includes(event.type)) {
      $('#run-title').textContent = event.type === 'lesson_start' ? `${event.class_title || 'Class'} · lesson ${event.lesson ?? '—'} of ${event.lessons_total ?? '—'}` : description;
    }
    if (event.type === 'lesson_start') state.progress.lessonWords = 0;
    if (event.type === 'lesson_answer') state.progress.lessonWords = event.words_learned ?? state.progress.lessonWords;
    if (event.type === 'lesson_complete') {
      state.progress.lessons++;
      state.progress.words += Number(event.words_learned) || 0;
      state.progress.lessonWords = 0;
    }
    if (event.type === 'result') {
      state.progress.lessons = event.lessons_completed ?? state.progress.lessons;
      state.progress.words = event.words_learned ?? state.progress.words;
      state.progress.lessonWords = 0;
      $('#run-title').textContent = description;
    }
    renderProgress();
  }
  function renderProgress() {
    const copy = $('#run-progress');
    copy.replaceChildren();
    if (state.selectedRun?.mode !== 'run') return;
    copy.append(element('span', '', `${state.progress.lessons} lessons complete`), element('span', '', `${state.progress.words} words learned`));
    if (state.progress.lessonWords) copy.append(element('span', '', `${state.progress.lessonWords} words in this lesson`));
  }
  function renderRun() {
    const run = state.selectedRun;
    if (!run) return;
    status($('#run-status'), statusLabels[run.status] || run.status, run.status === 'running' ? 'running' : run.status === 'completed' ? 'good' : ['failed', 'interrupted'].includes(run.status) ? 'bad' : '');
    const kind = runLabels[run.mode] || 'Run';
    const summary = typeof run.summary === 'string' ? run.summary : '';
    $('#run-meta').textContent = `${kind} · ${dateLabel(run.created_at)}${run.finished_at ? ` · finished ${dateLabel(run.finished_at)}` : ''}${run.source === 'schedule' ? ' · scheduled' : ''}`;
    if (run.status !== 'running') $('#run-title').textContent = summary || `${kind} ${statusLabels[run.status]?.toLowerCase() || run.status}.`;
    $('#cancel-run').hidden = run.status !== 'running';
    renderProgress();
  }
  function selectRun(run) {
    if (String(state.selectedRun?.id) === String(run.id)) return;
    clearTimeout(state.pollTimer);
    state.pollingGeneration++;
    state.selectedRun = run;
    state.cursor = 0;
    state.httpCount = 0;
    state.logTrimmed = false;
    state.progress = { lessons: 0, words: 0, lessonWords: 0 };
    $('#activity-log').replaceChildren();
    $('.run-details').open = ['failed', 'interrupted'].includes(run.status);
    $('#http-log').replaceChildren();
    $('#http-summary').textContent = 'HTTP requests (0)';
    message('#poll-error', '');
    $('#log-note')?.remove();
    $('#run-title').textContent = run.status === 'running' ? `${runLabels[run.mode] || 'Run'} in progress…` : 'Loading saved activity…';
    renderRun();
    renderHistory();
    pollRun(state.pollingGeneration);
  }
  async function pollRun(generation) {
    const run = state.selectedRun;
    if (!run || generation !== state.pollingGeneration) return;
    let delay = 1000;
    let continuePolling = false;
    try {
      const data = await api(`/api/runs/${encodeURIComponent(run.id)}?after=${state.cursor}`);
      if (generation !== state.pollingGeneration) return;
      if (!data?.run || !Array.isArray(data.events)) throw new Error('Run activity could not be read. Retrying shortly.');
      message('#poll-error', '');
      if (data.events.length && data.events[0].seq > state.cursor + 1) {
        showLogNote('Earlier events are no longer available. Showing the retained run log; progress counts may be partial until the final result.');
      }
      for (const item of data.events) {
        if (item.seq <= state.cursor) continue;
        handleEvent(item.event, item.seq);
        state.cursor = item.seq;
      }
      if (Number.isInteger(data.last_seq) && data.last_seq > state.cursor) state.cursor = data.last_seq;
      const finished = run.status === 'running' && data.run.status !== 'running';
      if (finished && ['failed', 'interrupted'].includes(data.run.status)) $('.run-details').open = true;
      state.selectedRun = data.run;
      const index = state.runs.findIndex(item => String(item.id) === String(data.run.id));
      if (index !== -1) state.runs[index] = data.run;
      if (data.run.status === 'running') state.activeRun = data.run;
      else if (String(state.activeRun?.id) === String(data.run.id)) state.activeRun = null;
      renderRun(); renderHistory(); updateRunControls();
      if (finished) refreshAfterRun();
      continuePolling = data.has_more || data.run.status === 'running';
      delay = data.has_more ? 0 : 1000;
    } catch (error) {
      if (generation !== state.pollingGeneration) return;
      message('#poll-error', `${error.message} Your run continues on the server. Retrying…`);
      delay = 3000;
      continuePolling = true;
    }
    if (continuePolling && generation === state.pollingGeneration) state.pollTimer = setTimeout(() => pollRun(generation), delay);
  }
  function refreshAfterRun() {
    loadClasses(); loadLibrary();
    if (!state.scheduleDirty) loadSchedule(false);
  }
  $('#refresh-runs').addEventListener('click', () => loadRuns({ initial: !state.selectedRun }));
  $('#cancel-run').addEventListener('click', async () => {
    const run = state.selectedRun;
    if (!run || !await confirmAction('Stop this run?', 'Completed lessons and collected words will stay saved. The current lesson may not finish.', 'Stop run')) return;
    const control = $('#cancel-run');
    control.disabled = true;
    try { await api(`/api/runs/${encodeURIComponent(run.id)}/cancel`, { method: 'POST' }); notify('Stop requested. Waiting for the worker to finish safely.'); }
    catch (error) { message('#poll-error', error.message); }
    finally { control.disabled = false; }
  });

  function exportLinks(kind, id) {
    const links = element('div', 'export-links');
    for (const [format, title] of [['md', 'MD'], ['json', 'JSON']]) {
      const link = element('a', '', title);
      link.href = exportURL(format, kind, id);
      link.setAttribute('aria-label', `Download ${kind} as ${format === 'md' ? 'Markdown' : 'JSON'}`);
      links.append(link);
    }
    return links;
  }
  async function loadTopic(topic, container) {
    container.replaceChildren(element('p', 'empty', 'Loading words…'));
    try {
      const data = await api(`/api/library/topics/${encodeURIComponent(topic.id)}`);
      if (!Array.isArray(data?.topic?.entries)) throw new Error('This topic could not be read.');
      container.replaceChildren();
      container.append(exportLinks('topic', topic.id));
      if (!data.topic.entries.length) {
        container.append(element('p', 'empty', 'No words in this wordset. Collect the class again to refresh it.'));
        container.dataset.loaded = 'true';
        return;
      }
      const table = element('table');
      const caption = element('caption', 'visually-hidden', data.topic.info?.name || topic.name || 'Vocabulary');
      const head = element('thead');
      const header = element('tr');
      for (const text of ['Word', 'Translation']) {
        const th = element('th', '', text); th.scope = 'col'; header.append(th);
      }
      head.append(header);
      const body = element('tbody');
      for (const entry of data.topic.entries) {
        const row = element('tr');
        row.append(element('td', '', entry.word ?? '—'), element('td', '', entry.translation ?? '—'));
        body.append(row);
      }
      table.append(caption, head, body);
      const wrapper = element('div', 'table-wrap'); wrapper.append(table); container.append(wrapper);
      container.dataset.loaded = 'true';
    } catch (error) { empty(container, 'Words unavailable', error.message, () => loadTopic(topic, container)); }
  }
  function renderLibrary() {
    const container = $('#library-collection');
    container.replaceChildren();
    if (!state.library.length) { empty(container, 'Your collection starts here.', 'Sync your classes, then choose Collect words below. Wordsets will be saved here for reading and download.'); updateSelection(); return; }
    let matches = 0;
    for (const item of filterLibrary(state.library, $('#library-search').value)) {
      const topics = item.topics;
      matches++;
      const section = element('section', 'panel library-class');
      const heading = element('div', 'panel-heading');
      const copy = element('div', 'class-selection');
      const select = element('input', 'class-select'); select.type = 'checkbox';
      select.setAttribute('aria-label', `Select shown wordsets in ${item.title || 'class'}`);
      select.addEventListener('change', () => changeSelection(topics.map(topic => topic.id), select.checked));
      const label = element('label', 'select-topic'); label.append(select);
      const title = element('div');
      title.append(element('h2', '', item.title || 'Untitled class'), element('p', '', `${topics.length} wordsets · collected ${dateLabel(item.collected_at)}`));
      copy.append(label, title);
      heading.append(copy);
      section.append(heading);
      if (!topics.length) section.append(element('p', 'empty', 'No topics collected for this class yet.'));
      for (const topic of topics) {
        const row = element('div', 'topic');
        const head = element('div', 'topic-head');
        const select = element('input', 'topic-select'); select.type = 'checkbox'; select.value = String(topic.id);
        select.setAttribute('aria-label', `Select ${topic.name || 'untitled wordset'}`);
        select.addEventListener('change', () => changeSelection([topic.id], select.checked));
        const label = element('label', 'select-topic'); label.append(select);
        const content = element('div', 'topic-content');
        content.id = `topic-${topic.id}`; content.hidden = true;
        const toggle = button('', async () => {
          content.hidden = !content.hidden;
          toggle.setAttribute('aria-expanded', String(!content.hidden));
          if (!content.hidden && !content.dataset.loaded && !content.dataset.loading) {
            content.dataset.loading = 'true';
            await loadTopic(topic, content);
            delete content.dataset.loading;
          }
        }, 'topic-toggle');
        toggle.setAttribute('aria-expanded', 'false'); toggle.setAttribute('aria-controls', content.id);
        toggle.append(element('span', 'topic-name', topic.name || 'Untitled topic'), element('span', 'topic-count', `${topic.count ?? 0} words`));
        head.append(label, toggle);
        row.append(head, content); section.append(row);
      }
      const exports = element('details', 'class-download');
      exports.append(element('summary', '', 'Download entire class'), exportLinks('class', item.class_id));
      section.append(exports);
      container.append(section);
    }
    if (!matches) empty(container, 'No matching wordsets.', 'Try a different name or clear your search. Your selection is kept when searching.');
    updateSelection();
  }
  async function loadLibrary() {
    try {
      const data = await api('/api/library');
      if (!Array.isArray(data?.classes)) throw new Error('The library could not be read.');
      state.library = data.classes;
      const available = new Set(state.library.flatMap(item => item.topics.map(topic => topic.id)));
      const kept = new Set([...state.selectedTopics].filter(id => available.has(id)));
      if (kept.size !== state.selectedTopics.size) message('#library-error', 'Some selected wordsets were refreshed or removed. Review your selection before downloading.');
      state.selectedTopics = kept;
      renderLibrary();
    } catch (error) { empty($('#library-collection'), 'Library unavailable', error.message, loadLibrary); }
  }
  $('#library-search').addEventListener('input', renderLibrary);

  function changeSelection(ids, checked) {
    try { state.selectedTopics = selectTopics(state.selectedTopics, ids, checked); message('#library-error', ''); }
    catch (error) { message('#library-error', error.message); }
    updateSelection();
  }
  function updateSelection() {
    const visible = filterLibrary(state.library, $('#library-search').value).flatMap(item => item.topics);
    const visibleSelected = visible.filter(topic => state.selectedTopics.has(topic.id)).length;
    const hidden = state.selectedTopics.size - visibleSelected;
    $('#selection-count').textContent = `${state.selectedTopics.size} selected${hidden ? ` · ${hidden} hidden by search` : ''}`;
    $('#select-visible').disabled = !visible.length || state.downloading;
    $('#clear-selection').disabled = !state.selectedTopics.size || state.downloading;
    $('#download-selection').disabled = $('#download-selection-json').disabled = !state.selectedTopics.size || state.downloading;
    $('#download-selection').textContent = state.downloading ? 'Preparing file…' : 'Download MD ↓';
    for (const input of $$('.topic-select')) {
      input.checked = state.selectedTopics.has(Number(input.value));
      input.disabled = state.downloading;
      input.closest('.topic').classList.toggle('selected', input.checked);
    }
    for (const input of $$('.class-select')) {
      const children = $$('.topic-select', input.closest('.library-class'));
      const checked = children.filter(child => child.checked).length;
      input.checked = children.length > 0 && checked === children.length;
      input.indeterminate = checked > 0 && checked < children.length;
      input.disabled = !children.length || state.downloading;
    }
  }
  $('#select-visible').addEventListener('click', () => changeSelection(filterLibrary(state.library, $('#library-search').value).flatMap(item => item.topics.map(topic => topic.id)), true));
  $('#clear-selection').addEventListener('click', () => { state.selectedTopics.clear(); message('#library-error', ''); updateSelection(); });
  async function downloadSelection(format) {
    if (state.downloading || !state.selectedTopics.size) return;
    state.downloading = true; updateSelection(); message('#library-error', '');
    try {
      const blob = await api('/api/library/export', { method: 'POST', body: { format, topics: [...state.selectedTopics] }, download: true });
      const url = URL.createObjectURL(blob);
      const link = element('a'); link.href = url; link.download = `vocabulary.${format}`;
      document.body.append(link); link.click(); link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 60000);
    } catch (error) { message('#library-error', `${error.message} Your selection has been kept. If words were recollected, refresh the page and select them again.`); }
    finally { state.downloading = false; updateSelection(); }
  }
  $('#download-selection').addEventListener('click', () => downloadSelection('md'));
  $('#download-selection-json').addEventListener('click', () => downloadSelection('json'));

  function scheduleValues() {
    return {
      enabled: $('#schedule-enabled').checked, time: $('#schedule-time').value,
      timezone: $('#schedule-timezone').value, days: $$('[name="day"]:checked').map(input => Number(input.value)), cron: $('#schedule-cron').value,
    };
  }
  function updateSchedulePreview() {
    const values = scheduleValues();
    $('#schedule-summary').textContent = scheduleSummary(values);
    const advanced = Boolean(values.cron.trim());
    $('#schedule-time').disabled = advanced;
    $$('[name="day"]').forEach(input => { input.disabled = advanced; });
    if (state.scheduleDirty) status($('#schedule-status'), 'Unsaved changes');
  }
  function renderSchedule(data, fill = true) {
    const schedule = normalizeSchedule(data);
    state.schedule = schedule;
    state.scheduleLoaded = true;
    if (fill) {
      $('#schedule-enabled').checked = Boolean(schedule.enabled);
      $('#schedule-time').value = schedule.time || '09:00';
      $('#schedule-timezone').value = schedule.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
      $$('[name="day"]').forEach(input => { input.checked = (schedule.days || [1, 2, 3, 4, 5]).includes(Number(input.value)); });
      $('#schedule-cron').value = schedule.cron || '';
      state.scheduleDirty = false;
      $('#schedule-fields').disabled = false;
      $('#save-schedule').disabled = false;
      updateSchedulePreview();
    }
    if (!state.scheduleDirty) status($('#schedule-status'), schedule.enabled ? 'Enabled' : 'Paused', schedule.enabled ? 'good' : '');
    $('#schedule-next').textContent = schedule.enabled ? schedule.next_run ? dateLabel(schedule.next_run, schedule.timezone) : 'Waiting for the next occurrence' : 'Paused';
    const statuses = { waiting: 'Waiting for the worker to be available', busy: 'Worker was busy; occurrence skipped', running: 'In progress', completed: 'Completed', failed: 'Failed', interrupted: 'Interrupted', cancelled: 'Stopped', skipped: 'Occurrence skipped', no_credentials: 'Lingos credentials are missing', disabled: 'Account is disabled' };
    $('#schedule-last').textContent = `${statuses[schedule.last_status] || schedule.last_status || 'No scheduled runs yet'}${schedule.last_error ? ` · ${schedule.last_error}` : ''}`;
    $('#schedule-history').textContent = schedule.last_run_id ? `View latest scheduled run →` : 'View run history →';
  }
  async function loadSchedule(fill = true) {
    if (fill) message('#schedule-error', '');
    try {
      const data = await api('/api/schedule');
      if (!data || typeof (data.schedule ?? data).enabled !== 'boolean') throw new Error('The saved schedule could not be read.');
      renderSchedule(data, fill);
      $('#retry-schedule').hidden = true;
    } catch (error) {
      if (fill) { message('#schedule-error', error.message); $('#retry-schedule').hidden = false; status($('#schedule-status'), 'Unavailable', 'bad'); }
    }
  }
  $('#schedule-form').addEventListener('input', () => { state.scheduleDirty = true; updateSchedulePreview(); });
  $('#retry-schedule').addEventListener('click', () => loadSchedule());
  $('#schedule-history').addEventListener('click', async () => {
    const id = state.schedule?.last_run_id;
    if (!id) return;
    const run = state.runs.find(item => String(item.id) === String(id));
    if (run) selectRun(run);
    else {
      try { const data = await api(`/api/runs/${encodeURIComponent(id)}?after=0`); if (data?.run) selectRun(data.run); }
      catch (error) { notify(error.message, true); }
    }
  });
  $('#schedule-form').addEventListener('submit', async event => {
    event.preventDefault();
    let payload;
    try { payload = schedulePayload(scheduleValues()); }
    catch (error) { message('#schedule-error', error.message); return; }
    const control = $('#save-schedule');
    control.disabled = true; control.textContent = 'Saving…';
    $('#schedule-fields').disabled = true;
    message('#schedule-error', '');
    try {
      const data = await api('/api/schedule', { method: 'PUT', body: payload });
      renderSchedule(data);
      notify('Schedule saved. It uses your saved lesson plan.');
    } catch (error) { message('#schedule-error', error.message); }
    finally { control.disabled = false; control.textContent = 'Save schedule'; $('#schedule-fields').disabled = false; updateSchedulePreview(); }
  });

  function renderCredentials() {
    const saved = Boolean(state.credentials?.saved);
    $('#setup-card').hidden = saved;
    status($('#credentials-state'), saved ? 'Saved · not verified' : 'Not saved', saved ? 'good' : '');
    status($('#credentials-overview'), saved ? 'Credentials saved · not verified' : 'Connect your account →', saved ? 'good' : '');
    $('#remove-credentials').disabled = !saved;
    $('#save-credentials').disabled = false;
    $('#credentials-fields').disabled = false;
    updateRunControls();
  }
  async function loadCredentials() {
    message('#credentials-error', '');
    try {
      const data = await api('/api/credentials');
      if (typeof data?.saved !== 'boolean') throw new Error('Your credential status could not be read.');
      state.credentials = data;
      $('#credentials-email').value = data.email || '';
      renderCredentials();
      $('#retry-credentials').hidden = true;
    } catch (error) {
      message('#credentials-error', error.message);
      status($('#credentials-state'), 'Unavailable', 'bad');
      status($('#credentials-overview'), 'Check account connection', 'bad');
      $('#retry-credentials').hidden = false;
      updateRunControls();
    }
  }
  $('#retry-credentials').addEventListener('click', loadCredentials);
  $('#credentials-form').addEventListener('submit', async event => {
    event.preventDefault();
    const payload = { email: $('#credentials-email').value.trim(), password: $('#credentials-password').value };
    if (!payload.password) return;
    await withForm(event.currentTarget, '#credentials-error', async () => {
      await api('/api/credentials', { method: 'PUT', body: payload });
      state.credentials = { saved: true, email: payload.email };
      resetSecrets($('#credentials-form'));
      notify('Lingos credentials saved. They have not been verified yet.');
    });
    renderCredentials();
  });
  $('#remove-credentials').addEventListener('click', async () => {
    if (!await confirmAction('Remove Lingos credentials?', 'New runs cannot connect until you save credentials again. Your library and history will stay saved.', 'Remove credentials')) return;
    await withForm($('#credentials-form'), '#credentials-error', async () => {
      await api('/api/credentials', { method: 'DELETE' });
      state.credentials = { saved: false, email: '' };
      $('#credentials-email').value = '';
      resetSecrets($('#credentials-form'));
      notify('Lingos credentials removed.');
    });
    renderCredentials();
  });
  $('#password-form').addEventListener('submit', async event => {
    event.preventDefault();
    if ($('#new-password').value !== $('#confirm-password').value) { message('#password-error', 'The new passwords do not match.'); $('#confirm-password').focus(); return; }
    const payload = { current_password: $('#current-password').value, password: $('#new-password').value };
    await withForm(event.currentTarget, '#password-error', async () => {
      await api('/api/account', { method: 'PATCH', body: payload });
      resetSecrets($('#password-form'));
      location.assign('/');
    });
  });
  async function loadAccount() {
    try {
      const data = await api('/api/account');
      account = data?.user;
      if (account) $('#account-identity').textContent = `Signed in as ${account.email}${account.admin ? ' · Administrator' : ''}`;
    } catch (error) { $('#account-identity').textContent = error.message; }
  }

  function makeSecretField(id, label) {
    const field = element('div', 'field');
    const caption = element('label', '', label); caption.htmlFor = id;
    const wrapper = element('div', 'secret-field');
    const input = element('input'); input.id = id; input.type = 'password'; input.autocomplete = 'new-password'; input.minLength = 12; input.maxLength = 1024; input.required = true;
    const reveal = element('button', '', 'Show'); reveal.type = 'button'; reveal.dataset.reveal = id; reveal.setAttribute('aria-controls', id); reveal.setAttribute('aria-pressed', 'false');
    wrapper.append(input, reveal); field.append(caption, wrapper); return field;
  }
  async function patchUser(user, body, control) {
    control.disabled = true;
    try {
      await api(`/api/admin/users/${encodeURIComponent(user.id)}`, { method: 'PATCH', body });
      notify('User updated.');
      await loadUsers();
    } catch (error) { notify(error.message, true); }
    finally { control.disabled = false; }
  }
  async function loadUsers() {
    const container = $('#users-list');
    if (!container) return;
    try {
      const data = await api('/api/admin/users');
      if (!Array.isArray(data?.users)) throw new Error('The user list could not be read.');
      container.replaceChildren();
      if (!data.users.length) { empty(container, 'No users found.', 'Use the form below to add a user.'); return; }
      for (const user of data.users) {
        const row = element('article', 'user-row');
        const head = element('div', 'user-row-head');
        const copy = element('div');
        copy.append(element('p', 'user-email', user.email), element('small', '', `${user.admin ? 'Administrator' : 'Member'}${String(user.id) === String(account?.id) ? ' · You' : ''}`));
        const badge = element('span'); status(badge, user.disabled ? 'Disabled' : 'Active', user.disabled ? 'bad' : 'good'); head.append(copy, badge);
        const details = element('details'); details.append(element('summary', '', 'Manage access'));
        const controls = element('div', 'user-controls');
        const toggle = button(user.disabled ? 'Enable account' : 'Disable account', async () => {
          if (!user.disabled && !await confirmAction('Disable this account?', `${user.email} will be signed out and unable to sign in. You can enable access again later.`, 'Disable account')) return;
          await patchUser(user, { disabled: !user.disabled }, toggle);
        }, `button${user.disabled ? '' : ' danger'}`);
        const admin = button(user.admin ? 'Remove administrator access' : 'Make administrator', async () => {
          if (!await confirmAction(user.admin ? 'Remove administrator access?' : 'Grant administrator access?', user.admin ? `${user.email} will no longer be able to manage users.` : `${user.email} will be able to create, disable and reset passwords for other users.`, user.admin ? 'Remove access' : 'Grant access')) return;
          await patchUser(user, { admin: !user.admin }, admin);
        });
        controls.append(toggle, admin); details.append(controls);
        const form = element('form', 'user-reset');
        form.append(makeSecretField(`reset-password-${user.id}`, 'New workspace password'));
        const hint = element('p', 'footnote', 'At least 12 characters. All sessions for this user will be signed out.');
        const errorNode = element('p', 'message error'); errorNode.setAttribute('role', 'alert'); errorNode.hidden = true;
        const submit = element('button', 'button', 'Reset password'); submit.type = 'submit';
        form.append(hint, errorNode, submit);
        form.addEventListener('submit', async event => {
          event.preventDefault();
          const password = $('input', form).value;
          if (!await confirmAction('Reset this password?', `${user.email} will need the new password to sign in. All existing sessions will end.`, 'Reset password')) return;
          await withForm(form, errorNode, async () => {
            await api(`/api/admin/users/${encodeURIComponent(user.id)}`, { method: 'PATCH', body: { password } });
            resetSecrets(form);
            if (String(user.id) === String(account?.id)) location.assign('/');
            else notify('Password reset. Share the new password securely.');
          });
        });
        details.append(form); row.append(head, details); container.append(row);
      }
    } catch (error) { empty(container, 'Users unavailable', error.message, loadUsers); }
  }
  $('#refresh-users')?.addEventListener('click', loadUsers);
  $('#create-user-form')?.addEventListener('submit', async event => {
    event.preventDefault();
    const form = event.currentTarget;
    const payload = { email: $('#user-email').value.trim(), password: $('#user-password').value, admin: $('#user-admin').checked };
    await withForm(form, '#users-error', async () => {
      await api('/api/admin/users', { method: 'POST', body: payload });
      form.reset(); resetSecrets(form);
      notify('User created. Share the initial password securely.');
      await loadUsers();
    });
  });

  // A lightweight history check discovers scheduled runs without changing the selected history item.
  async function watchRuns() {
    if (watchingRuns) return;
    watchingRuns = true;
    clearTimeout(state.watchTimer);
    if (!document.hidden) await loadRuns({ quiet: true });
    watchingRuns = false;
    state.watchTimer = setTimeout(watchRuns, 10000);
  }
  window.addEventListener('beforeunload', event => {
    if (!settingsDirty() && !state.scheduleDirty) return;
    event.preventDefault(); event.returnValue = '';
  });
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) { clearTimeout(state.watchTimer); watchRuns(); }
  });
  Promise.allSettled([loadSettings(), loadClasses(), loadCredentials(), loadLibrary(), loadSchedule(), loadRuns({ initial: true }), loadAccount().then(loadUsers)]).then(() => { state.watchTimer = setTimeout(watchRuns, 10000); });
}
