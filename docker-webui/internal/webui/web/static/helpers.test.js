import test from 'node:test';
import assert from 'node:assert/strict';
import { classCounts, settingsPayload, settingsSignature, schedulePayload, normalizeSchedule, scheduleSummary, httpDescription, eventDescription, durationLabel, lessonReport, dateLabel, exportURL, passwordError, filterLibrary, selectTopics } from './helpers.js';

test('class overrides preserve titles, skip blank defaults, accept zero and reject duplicate or excessive counts', () => {
  assert.deepEqual({ ...classCounts([{ title: 'My class', count: '' }, { title: 'Skipped', count: '0' }]) }, { Skipped: 0 });
  assert.throws(() => classCounts([{ title: 'English', count: 1 }, { title: 'ENGLISH', count: 2 }]), /more than once/);
  assert.throws(() => classCounts([{ title: 'A', count: 10000 }, { title: 'B', count: 1 }]), /total/);
  assert.throws(() => classCounts([{ title: '', count: 2 }]), /title/);
  assert.throws(() => classCounts([{ title: 'A', count: '-1' }]), /whole number/);
  assert.equal(Object.getPrototypeOf(classCounts([{ title: '__proto__', count: 1 }])), null);
});

test('settings enforce integer bounds and ordered timing, and send the complete worker shape', () => {
  const payload = settingsPayload({}, []);
  assert.equal(payload.lessonCount, 1);
  assert.equal(payload.maxRunSeconds, 900);
  assert.equal(Object.keys(payload).length, 8);
  assert.throws(() => settingsPayload({ answerDelayMinMs: 10, answerDelayMaxMs: 2 }, []), /Maximum answer/);
  assert.throws(() => settingsPayload({ errorsPer100Words: 101 }, []));
  assert.throws(() => settingsPayload({ lessonCount: '' }, []));
});

test('schedule payload contains only writable fields and validates timezone, weekdays and cron shape', () => {
  const values = { enabled: true, time: '09:00', timezone: 'Europe/London', days: [1, 2, 3, 4, 5], cron: '', next_run: 123 };
  assert.equal(scheduleSummary(values), 'Mon, Tue, Wed, Thu, Fri at 9:00 am · Europe/London');
  assert.equal(Object.keys(schedulePayload(values)).length, 5);
  assert.throws(() => schedulePayload({ ...values, days: [] }), /weekday/);
  assert.throws(() => schedulePayload({ ...values, timezone: 'not-a-zone' }), /timezone/);
  assert.throws(() => schedulePayload({ ...values, cron: '* * *' }), /five/);
  assert.equal(normalizeSchedule({ nextRun: 123, lastRunId: 4 }).last_run_id, 4);
  assert.equal(normalizeSchedule({ schedule: { next_run: 123 } }).next_run, 123);
});

test('HTTP detail never displays a URL, path or unrecognized semantic operation', () => {
  const result = httpDescription({ method: 'POST', operation: 'submit_answer', status: 200, duration_ms: 12.4, path: '/secret?password=123', url: 'https://private' });
  assert.deepEqual(result, { title: 'Submit answer · POST', detail: 'Status 200 · 12 ms' });
  assert.deepEqual(httpDescription({ method: 'invalid', operation: 'https://secret', error: 'timeout' }), { title: 'Service request · HTTP', detail: 'Timed out' });
});

test('progress uses known event fields without HTTP spam and timestamps are seconds', () => {
  assert.equal(eventDescription({ type: 'lesson_start', class_title: 'English', lesson: 2, lessons_total: 3 }), 'English · lesson 2 of 3 started.');
  assert.equal(eventDescription({ type: 'class_skipped', class_title: 'English', reason: 'disabled' }), 'English · skipped by your plan.');
  assert.equal(eventDescription({ type: 'lesson_answer', word: '<script>' }), null);
  assert.notEqual(dateLabel(1800000000), '—');
  assert.equal(dateLabel(0), '—');
  assert.equal(exportURL('md', 'class', '12&topic=4'), '/api/library/export?format=md&class=12%26topic%3D4');
});

test('duration labels round seconds, use compact units and reject unknown or invalid values', () => {
  for (const [seconds, label] of [[0, '0s'], [0.4, '0s'], [0.5, '1s'], [59, '59s'], [59.5, '1m'], [60, '1m'], [65, '1m 5s'], [3599, '59m 59s'], [3599.5, '1h'], [3600, '1h'], [3665, '1h 1m'], [86400, '24h']]) {
    assert.equal(durationLabel(seconds), label);
  }
  for (const seconds of [undefined, null, '', '65', NaN, Infinity, -Infinity, -1, -0.1, true, {}]) assert.equal(durationLabel(seconds), '—');
});

test('missing final reports never invent totals but may show elapsed run duration', () => {
  for (const result of [undefined, null]) {
    const report = { metrics: [], note: 'No final worker report was saved. Totals are unavailable.', challenge: null };
    assert.deepEqual(lessonReport(undefined, result), report);
    assert.deepEqual(lessonReport({ created_at: 100, finished_at: 165 }, result), { ...report, metrics: [{ label: 'Duration', value: '1m 5s' }] });
  }
});

test('sparse reports include only finite nonnegative numbers, without coercing strings or inventing fields', () => {
  assert.deepEqual(lessonReport({}, {}), { metrics: [], note: '', challenge: null });
  assert.deepEqual(lessonReport(null, { lessons_completed: 2, words_learned: '30', duration_seconds: Infinity, errors_made: -1, classes_limited: null, classes_empty: NaN }).metrics, [{ label: 'Lessons', value: '2' }]);
  for (const value of [undefined, null, '', '0', true, false, NaN, Infinity, -Infinity, -1, {}, []]) {
    assert.deepEqual(lessonReport({}, { lessons_completed: value, words_learned: value, duration_seconds: value, errors_made: value, classes_limited: value, classes_empty: value }).metrics, []);
  }
  assert.deepEqual(lessonReport(null, { words_learned: 2.5 }).metrics, [{ label: 'Words learned', value: '2.5' }]);
});

test('explicit zero metrics remain visible in dashboard order', () => {
  assert.deepEqual(lessonReport({ created_at: 100, finished_at: 165 }, { lessons_completed: 0, words_learned: 0, duration_seconds: 0, errors_made: 0, classes_limited: 0, classes_empty: 0 }).metrics,
    ['Lessons', 'Words learned', 'Duration', 'Errors', 'Classes at limit', 'Empty classes'].map(label => ({ label, value: label === 'Duration' ? '0s' : '0' })));
});

test('elapsed duration is a fallback only, with finite positive ordered timestamps', () => {
  const run = { created_at: 100, finished_at: 165 };
  for (const duration_seconds of [undefined, null, '1', -1, NaN, Infinity]) {
    assert.deepEqual(lessonReport(run, { duration_seconds }).metrics, [{ label: 'Duration', value: '1m 5s' }]);
  }
  assert.deepEqual(lessonReport(run, { duration_seconds: 5 }).metrics, [{ label: 'Duration', value: '5s' }]);
  assert.deepEqual(lessonReport({ created_at: 100, finished_at: 100 }, {}).metrics, [{ label: 'Duration', value: '0s' }]);
  for (const [created_at, finished_at] of [[0, 100], [100, 0], [-1, 100], [100, 99], [100, Infinity], [NaN, 100], [Infinity, Infinity], ['100', 165], [100, '165'], [undefined, 165], [100, null]]) {
    assert.deepEqual(lessonReport({ created_at, finished_at }, {}).metrics, []);
  }
});

test('only failed reports display a string worker message without inventing totals', () => {
  const report = lessonReport({ created_at: 100, finished_at: 105 }, { success: false, message: 'Sign-in failed.' });
  assert.deepEqual(report, { metrics: [{ label: 'Duration', value: '5s' }], note: 'Sign-in failed.', challenge: null });
  for (const result of [{ success: true, message: 'Done' }, { message: 'Not a failure' }, { success: false }, { success: false, message: {} }]) assert.equal(lessonReport(null, result).note, '');
});

test('challenge snapshots describe only the last observed class and warn when subsequent lessons ran', () => {
  const result = { lessons_completed: 3, challenge_snapshot: {
    group_id: '9223372036854775807', lessons_completed_at_snapshot: 2, available: 1, in_progress: 1, completed: 1,
    challenges: [{ title: 'First', status: 'available', points: 0 }, { title: 'Second', status: 'in_progress', points: 20 }, { title: 'Third', status: 'completed', points: -1 }],
  } };
  const before = structuredClone(result);
  assert.deepEqual(lessonReport(null, result).challenge, {
    title: 'Wyzwania · class 9223372036854775807',
    note: 'Last check for this class only. Not live. More lessons ran after this check.',
    counts: '1 available · 1 in progress · 1 completed',
    items: ['First · available · 0 points', 'Second · in progress · 20 points', 'Third · completed · -1 points'],
  });
  assert.deepEqual(result, before);
});

test('unknown snapshot freshness never promises the final state', () => {
  for (const [observed, total] of [[undefined, 2], [2, undefined], ['2', 2], [2, '2'], [NaN, 2], [-1, 2]]) {
    const { challenge } = lessonReport(null, { lessons_completed: total, challenge_snapshot: { group_id: '7', lessons_completed_at_snapshot: observed } });
    assert.match(challenge.note, /Last check for this class only. Not live/);
    assert.match(challenge.note, /Final state not confirmed/);
    assert.doesNotMatch(challenge.note, /More lessons ran/);
  }
  const { challenge } = lessonReport(null, { lessons_completed: 0, challenge_snapshot: { group_id: '7', lessons_completed_at_snapshot: 0 } });
  assert.equal(challenge.note, 'Last check for this class only. Not live.');
});

test('missing challenge snapshots and unknown counts differ from an observed zero available count', () => {
  for (const challenge_snapshot of [undefined, null, false, 'missing', []]) assert.equal(lessonReport(null, { challenge_snapshot }).challenge, null);
  const unknown = lessonReport(null, { challenge_snapshot: { group_id: '7', available: '0', in_progress: -1, completed: Infinity } }).challenge;
  assert.equal(unknown.counts, '');
  assert.deepEqual(unknown.items, []);
  const zero = lessonReport(null, { challenge_snapshot: { group_id: '7', available: 0 } }).challenge;
  assert.equal(zero.counts, '0 available');
  assert.deepEqual(zero.items, []);
});

test('untrusted challenge and failure text stays an inert string, without HTML escaping or object coercion', () => {
  const text = '<img src=x onerror=alert(1)> &lt;script&gt; "&';
  const { note, challenge } = lessonReport(null, { success: false, message: text, challenge_snapshot: {
    group_id: text, challenges: [{ title: text, status: 'in_progress', points: 0 }, null, { title: {}, status: '__proto__', points: '5' }],
  } });
  assert.equal(note, text);
  assert.equal(challenge.title, `Wyzwania · class ${text}`);
  assert.deepEqual(challenge.items, [`${text} · in progress · 0 points`, 'Challenge']);
});

test('settings dirty state ignores class key order and passwords enforce rune and UTF-8 limits', () => {
  assert.equal(settingsSignature({ classLessonCounts: { b: 2, a: 1 } }), settingsSignature({ classLessonCounts: { a: 1, b: 2 } }));
  assert.equal(passwordError('short-legacy'), '');
  assert.match(passwordError('short', true), /12/);
  assert.match(passwordError('é'.repeat(513)), /1,024/);
  assert.equal(passwordError('é'.repeat(512)), '');
  assert.match(passwordError('😀'.repeat(6), true), /12/);
});

test('library search matches wordsets or their class without changing the stored library', () => {
  const library = [{ title: 'English', topics: [{ id: 1, name: 'Food' }, { id: 2, name: 'Travel' }] }, { title: 'French', topics: [{ id: 3, name: 'Food' }] }];
  assert.deepEqual(filterLibrary(library, ' FOOD ').flatMap(item => item.topics.map(topic => topic.id)), [1, 3]);
  assert.deepEqual(filterLibrary(library, 'english').flatMap(item => item.topics.map(topic => topic.id)), [1, 2]);
  assert.equal(filterLibrary(library, 'missing').length, 0);
  assert.equal(library[0].topics.length, 2);
});

test('batch selection spans filters, deduplicates and rejects overflow atomically', () => {
  let selected = selectTopics(new Set([1]), [1, 2, 3], true);
  selected = selectTopics(selected, [2], false);
  assert.deepEqual([...selected], [1, 3]);
  const full = new Set(Array.from({ length: 500 }, (_, i) => i + 1));
  assert.equal(selectTopics(full, [1], true).size, 500);
  assert.throws(() => selectTopics(full, [501], true), /500/);
  assert.equal(full.size, 500);
});
