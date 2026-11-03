(() => {
  const csrf = document.querySelector('#csrf-token')?.value;
  if (!csrf) return;

  const output = document.querySelector('#worker-output');
  const title = document.querySelector('#terminal-title');
  const indicator = document.querySelector('#live-indicator');
  const toast = document.querySelector('#toast');
  let running = false;
  let toastTimer;

  function notify(message) {
    toast.textContent = message;
    toast.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toast.classList.remove('show'), 3200);
  }

  function showPage() {
    const name = location.hash.slice(1);
    const page = document.querySelector(`[data-page][id="${['worker', 'vocabulary', 'schedule', 'account'].includes(name) ? name : 'worker'}"]`);
    document.querySelectorAll('[data-page]').forEach(item => { item.hidden = item !== page; });
    document.querySelectorAll('[data-page-link]').forEach(link => {
      if (link.dataset.pageLink === page.id) link.setAttribute('aria-current', 'page');
      else link.removeAttribute('aria-current');
    });
    document.querySelector('#activity').hidden = !['worker', 'vocabulary'].includes(page.id);
    document.title = `${page.querySelector('h1').textContent} · LingosBot`;
  }
  window.addEventListener('hashchange', showPage);
  const params = new URLSearchParams(location.search);
  if (params.has('saved')) {
    location.hash = 'account';
    notify('Credentials saved.');
  } else if (params.has('schedule_saved')) {
    location.hash = 'schedule';
    notify('Schedule saved.');
  }
  if (params.has('saved') || params.has('schedule_saved')) history.replaceState(null, '', location.pathname + location.hash);
  showPage();

  function addLine(message, kind = '') {
    const line = document.createElement('div');
    line.className = `terminal-line ${kind}`;
    const time = document.createElement('span');
    time.className = 'terminal-time';
    time.textContent = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    line.append(time, document.createTextNode(message));
    output.append(line);
    output.scrollTop = output.scrollHeight;
  }

  function downloadVocabulary(result) {
    const scrape = result.scrape;
    if (!scrape) throw new Error('Worker returned no vocabulary.');
    const cell = value => String(value ?? '').replaceAll('|', '\\|').replace(/[\r\n]+/g, ' ');
    let markdown = `# ${cell(scrape.class?.title || 'Vocabulary')}\n\nClass ID: ${cell(scrape.class?.group_id)}\n`;
    for (const set of scrape.wordsets || []) {
      markdown += `\n## ${cell(set.info?.name || 'Word set')}\n\n| Word | Translation |\n| --- | --- |\n`;
      for (const entry of set.entries || []) markdown += `| ${cell(entry.word)} | ${cell(entry.translation)} |\n`;
    }
    const url = URL.createObjectURL(new Blob([markdown], { type: 'text/markdown;charset=utf-8' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = `lingos-class-${scrape.class?.group_id || 'vocabulary'}.md`;
    link.click();
    URL.revokeObjectURL(url);
  }

  function showClasses(classes) {
    const container = document.querySelector('#class-results');
    container.replaceChildren();
    if (!classes.length) {
      const empty = document.createElement('p');
      empty.className = 'empty';
      empty.textContent = 'No classes found.';
      container.append(empty);
      return;
    }
    const list = document.createElement('div');
    list.className = 'class-list';
    for (const item of classes) {
      const row = document.createElement('div');
      row.className = 'class-row';
      const copy = document.createElement('span');
      const name = document.createElement('b');
      name.textContent = item.title || 'Untitled class';
      const id = document.createElement('small');
      id.textContent = `Class ID: ${item.group_id}`;
      copy.append(name, id);
      const select = document.createElement('button');
      select.type = 'button';
      select.textContent = 'Use ID';
      select.addEventListener('click', () => {
        document.querySelector('#class-id').value = item.group_id;
        document.querySelector('#class-id').focus();
      });
      row.append(copy, select);
      list.append(row);
    }
    container.append(list);
  }

  async function startWorker(mode) {
    if (running) return;
    const query = new URLSearchParams({ mode });
    if (mode === 'scrape') {
      const id = document.querySelector('#class-id');
      if (!/^[1-9][0-9]{0,18}$/.test(id.value.trim())) {
        id.focus();
        notify('Enter a valid numeric class ID.');
        return;
      }
      query.set('class', id.value.trim());
    }
    if (mode === 'run') {
      const lessons = document.querySelector('#lesson-count');
      const errors = document.querySelector('#error-rate');
      if (!lessons.reportValidity() || !errors.reportValidity()) return;
      query.set('lessons', lessons.value);
      query.set('errors', errors.value);
    }
    running = true;
    document.querySelectorAll('[data-action]').forEach(button => { button.disabled = true; });
    output.replaceChildren();
    title.textContent = mode === 'classes' ? 'Syncing classes…' : mode === 'scrape' ? 'Collecting vocabulary…' : 'Running worker…';
    indicator.textContent = 'Running';
    let result = false;
    let success = false;
    try {
      const response = await fetch(`/events?${query}`, { headers: { 'X-CSRF-Token': csrf } });
      if (!response.ok) throw new Error((await response.text()).trim() || 'Request failed.');
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      let done = false;
      while (!done) {
        const part = await reader.read();
        done = part.done;
        buffer += decoder.decode(part.value || new Uint8Array(), { stream: !done });
        const blocks = buffer.split('\n\n');
        buffer = blocks.pop();
        for (const block of blocks) {
          const raw = block.split('\n').find(line => line.startsWith('data: '))?.slice(6);
          if (!raw) continue;
          let event;
          try { event = JSON.parse(raw); } catch { addLine('Unreadable worker event.', 'error'); continue; }
          if (event.type === 'login_success') addLine('Connected to Lingos.');
          else if (event.type === 'http_request') addLine(`${event.method || 'HTTP'} request · ${event.status ?? 'connection issue'}`);
          else if (event.type === 'lesson_answer') addLine(`${event.correct ? 'Correct' : 'Answer checked'} · ${event.word || 'Vocabulary item'}`, event.correct ? 'good' : '');
          else if (event.type === 'result') {
            result = true;
            if (event.success) {
              if (mode === 'classes') showClasses(event.classes || []);
              if (mode === 'scrape') downloadVocabulary(event);
              success = true;
              const summary = mode === 'run' ? `${event.lessons_completed || 0} lesson(s) completed · ${event.words_learned || 0} words learned` : mode === 'classes' ? `${event.classes?.length || 0} class(es) synced.` : 'Vocabulary downloaded.';
              title.textContent = summary;
              addLine(summary, 'good');
            } else {
              title.textContent = 'Task failed.';
              addLine(event.message || event.error_code || 'Worker returned an error.', 'error');
            }
          } else addLine(event.message || 'Worker event received.', event.type === 'error' || !event.type ? 'error' : '');
        }
      }
      if (!result) {
        title.textContent = 'Worker ended without a result.';
        addLine('Check the worker and try again.', 'error');
      }
      indicator.textContent = !result ? 'Incomplete' : success ? 'Complete' : 'Error';
    } catch (error) {
      title.textContent = 'Task failed.';
      addLine(error.message || 'Could not connect to the worker.', 'error');
      indicator.textContent = 'Error';
    } finally {
      running = false;
      document.querySelectorAll('[data-action]').forEach(button => { button.disabled = false; });
    }
  }

  document.querySelectorAll('[data-action]').forEach(button => button.addEventListener('click', () => startWorker(button.dataset.action)));
})();
