export const settingLimits = {
  lessonCount: [1, 10000, 1],
  lessonPromptSafetyCap: [1, 10000, 30],
  errorsPer100Words: [0, 100, 0],
  answerDelayMinMs: [0, 60000, 0],
  answerDelayMaxMs: [0, 60000, 0],
  requestTimeoutSeconds: [1, 300, 30],
  maxRunSeconds: [1, 86400, 900],
};

export function classCounts(rows) {
  const result = Object.create(null);
  const seen = new Set();
  let total = 0;
  for (const { title, count } of rows) {
    if (String(count).trim() === '' && !title.trim()) continue;
    if (String(count).trim() === '') continue;
    const name = title.trim();
    if (!name) throw new Error('Give every manual class override a title.');
    const key = name.normalize('NFKC').toLocaleLowerCase('und');
    if (seen.has(key)) throw new Error(`“${name}” appears more than once. Use one override per class title.`);
    seen.add(key);
    const value = Number(count);
    if (!Number.isInteger(value) || value < 0 || value > 10000) throw new Error(`The lesson count for “${name}” must be a whole number from 0 to 10,000.`);
    total += value;
    result[name] = value;
  }
  if (total > 10000) throw new Error('Class overrides cannot total more than 10,000 lessons.');
  return result;
}

export function settingsPayload(values, rows) {
  const result = {};
  for (const [key, [min, max, fallback]] of Object.entries(settingLimits)) {
    const raw = values[key] ?? fallback;
    const value = Number(raw);
    if (String(raw).trim() === '' || !Number.isInteger(value) || value < min || value > max) {
      throw new Error('Check your lesson plan. All limits must be whole numbers within the displayed range.');
    }
    result[key] = value;
  }
  if (result.answerDelayMinMs > result.answerDelayMaxMs) throw new Error('Maximum answer delay must be at least the minimum delay.');
  result.classLessonCounts = classCounts(rows);
  return result;
}

export function schedulePayload(values) {
  const result = {
    enabled: Boolean(values.enabled),
    time: values.time,
    timezone: values.timezone.trim(),
    days: [...new Set(values.days.map(Number))].sort((a, b) => a - b),
    cron: values.cron.trim(),
  };
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(result.time)) throw new Error('Choose a valid start time.');
  try { new Intl.DateTimeFormat('en', { timeZone: result.timezone }).format(); }
  catch { throw new Error('Enter a valid IANA timezone, such as Europe/London.'); }
  if (!result.timezone || result.timezone === 'Local') throw new Error('Choose a named timezone, such as Europe/London.');
  if (!result.days.length || result.days.some(day => !Number.isInteger(day) || day < 0 || day > 6)) throw new Error('Choose at least one weekday.');
  if (result.cron && result.cron.split(/\s+/).length !== 5) throw new Error('Cron expressions must contain exactly five fields.');
  return result;
}

export function normalizeSchedule(data) {
  const value = data.schedule ?? data;
  return {
    ...value,
    next_run: value.next_run ?? value.nextRun,
    last_run_id: value.last_run_id ?? value.lastRunId,
    last_status: value.last_status ?? value.lastStatus,
    last_error: value.last_error ?? value.lastError,
  };
}

const days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
export function scheduleSummary(schedule) {
  const zone = schedule.timezone || 'your timezone';
  if (schedule.cron?.trim()) return `${schedule.enabled ? 'Runs' : 'Paused'} · cron ${schedule.cron.trim()} · ${zone}`;
  const chosen = (schedule.days || []).map(Number);
  const repeat = chosen.length === 7 ? 'Every day' : [1, 2, 3, 4, 5, 6, 0].filter(day => chosen.includes(day)).map(day => days[day]).join(', ') || 'Choose weekdays';
  const [hour, minute] = (schedule.time || '09:00').split(':').map(Number);
  const time = `${hour % 12 || 12}:${String(minute).padStart(2, '0')} ${hour < 12 ? 'am' : 'pm'}`;
  return `${schedule.enabled ? repeat : `Paused · ${repeat}`} at ${time} · ${zone}`;
}

const operations = {
  sign_in: 'Sign in',
  load_dashboard: 'Load dashboard',
  load_vocabulary: 'Load vocabulary',
  start_lesson: 'Start lesson',
  submit_answer: 'Submit answer',
  continue_lesson: 'Continue lesson',
  select_class: 'Select class',
  join_challenge: 'Join challenge',
  load_page: 'Load page',
};
const transportErrors = { timeout: 'Timed out', connection: 'Connection failed', request: 'Request failed' };
export function httpDescription(event) {
  const method = /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)$/.test(event.method) ? event.method : 'HTTP';
  const operation = operations[event.operation] || 'Service request';
  const status = Number.isInteger(event.status) && event.status >= 100 && event.status <= 599 ? `Status ${event.status}` : transportErrors[event.error] || 'No response';
  const duration = typeof event.duration_ms === 'number' && Number.isFinite(event.duration_ms) && event.duration_ms >= 0 ? ` · ${Math.round(event.duration_ms)} ms` : '';
  return { title: `${operation} · ${method}`, detail: `${status}${duration}` };
}

const skippedReasons = { disabled: 'skipped by your plan', empty_vocabulary: 'no vocabulary available', lesson_limit: 'lesson limit reached' };
export function eventDescription(event) {
  const title = event.class_title || 'Class';
  const lesson = `${event.lesson ?? '—'} of ${event.lessons_total ?? '—'}`;
  switch (event.type) {
    case 'login_success': return 'Connected to your learning account.';
    case 'class_start': return `${title} · starting ${event.lessons_total ?? '—'} lessons.`;
    case 'lesson_start': return `${title} · lesson ${lesson} started.`;
    case 'lesson_complete': return `${title} · lesson ${lesson} complete · ${event.words_learned ?? 0} words learned.`;
    case 'class_complete': return `${title} · ${event.lessons_completed ?? 0} lessons completed.`;
    case 'class_skipped': return `${title} · ${skippedReasons[event.reason] || 'skipped'}.`;
    case 'result':
      if (!event.success) return event.message || 'The run could not be completed.';
      if (event.scrape) return 'Words collected and saved to your library.';
      if (Array.isArray(event.classes)) return `${event.classes.length} classes synced.`;
      return `${event.lessons_completed ?? 0} lessons completed · ${event.words_learned ?? 0} words learned.`;
    case 'error': return event.message || 'The worker reported an error.';
    default: return null;
  }
}

const nonnegativeNumber = value => Number.isFinite(value) && value >= 0;
export function durationLabel(seconds) {
  if (!nonnegativeNumber(seconds)) return '—';
  const total = Math.round(seconds);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor(total % 3600 / 60);
  if (hours) return `${hours}h${minutes ? ` ${minutes}m` : ''}`;
  if (minutes) return `${minutes}m${total % 60 ? ` ${total % 60}s` : ''}`;
  return `${total}s`;
}

export function lessonReport(run, result) {
  const duration = nonnegativeNumber(result?.duration_seconds) ? result.duration_seconds
    : Number.isFinite(run?.created_at) && run.created_at > 0 && Number.isFinite(run?.finished_at) && run.finished_at > 0 && run.finished_at >= run.created_at
      ? run.finished_at - run.created_at : undefined;
  const metrics = [
    ['Lessons', result?.lessons_completed], ['Words learned', result?.words_learned], ['Duration', duration],
    ['Errors', result?.errors_made], ['Classes at limit', result?.classes_limited], ['Empty classes', result?.classes_empty],
  ].filter(([, value]) => nonnegativeNumber(value)).map(([label, value]) => ({ label, value: label === 'Duration' ? durationLabel(value) : String(value) }));
  const note = !result ? 'No final worker report was saved. Totals are unavailable.'
    : result.success === false && typeof result.message === 'string' ? result.message : '';
  let challenge = null;
  const snapshot = result?.challenge_snapshot;
  if (snapshot && typeof snapshot === 'object' && !Array.isArray(snapshot)) {
    const group = typeof snapshot.group_id === 'string' || Number.isFinite(snapshot.group_id) ? snapshot.group_id : '—';
    let snapshotNote = 'Last check for this class only. Not live.';
    if (!nonnegativeNumber(snapshot.lessons_completed_at_snapshot) || !nonnegativeNumber(result.lessons_completed)) {
      snapshotNote += ' Final state not confirmed.';
    } else if (snapshot.lessons_completed_at_snapshot < result.lessons_completed) {
      snapshotNote += ' More lessons ran after this check.';
    }
    challenge = {
      title: `Wyzwania · class ${group}`,
      note: snapshotNote,
      counts: [['available', snapshot.available], ['in progress', snapshot.in_progress], ['completed', snapshot.completed]]
        .filter(([, value]) => nonnegativeNumber(value)).map(([label, value]) => `${value} ${label}`).join(' · '),
      items: (Array.isArray(snapshot.challenges) ? snapshot.challenges : []).filter(item => item && typeof item === 'object' && !Array.isArray(item)).map(item => [
        typeof item.title === 'string' ? item.title : 'Challenge',
        ['available', 'in_progress', 'completed'].includes(item.status) ? item.status.replace('_', ' ') : '',
        Number.isFinite(item.points) ? `${item.points} points` : '',
      ].filter(Boolean).join(' · ')),
    };
  }
  return { metrics, note, challenge };
}

export function dateLabel(seconds, timezone) {
  if (!seconds) return '—';
  const date = new Date(Number(seconds) * 1000);
  if (!Number.isFinite(date.getTime())) return '—';
  const options = { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', ...(timezone ? { timeZone: timezone, timeZoneName: 'short' } : {}) };
  try { return new Intl.DateTimeFormat('en', options).format(date); }
  catch { return new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }).format(date); }
}

export function exportURL(format, kind, id) {
  const params = new URLSearchParams({ format });
  if (kind) params.set(kind, String(id));
  return `/api/library/export?${params}`;
}

export function passwordError(password, minimum = false) {
  if (new TextEncoder().encode(password).length > 1024) return 'Passwords must be at most 1,024 bytes. Use a shorter password.';
  if (minimum && [...password].length < 12) return 'Use at least 12 characters for a workspace password.';
  return '';
}

export function settingsSignature(settings) {
  return JSON.stringify({ ...settings, classLessonCounts: Object.entries(settings.classLessonCounts || {}).sort(([a], [b]) => a.localeCompare(b)) });
}

export function filterLibrary(classes, search) {
  const query = search.trim().toLocaleLowerCase('en');
  return classes.map(item => ({ ...item, topics: (item.topics || []).filter(topic =>
    !query || (item.title || '').toLocaleLowerCase('en').includes(query) || (topic.name || '').toLocaleLowerCase('en').includes(query))
  })).filter(item => !query || item.topics.length);
}

export function selectTopics(selected, ids, checked) {
  const result = new Set(selected);
  for (const id of ids) {
    if (checked) result.add(id);
    else result.delete(id);
  }
  if (result.size > 500) throw new Error('Choose up to 500 wordsets per download. Clear your selection to start another batch.');
  return result;
}
