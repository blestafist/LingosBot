import test from 'node:test';
import assert from 'node:assert/strict';
import { classCounts, settingsPayload, settingsSignature, schedulePayload, normalizeSchedule, scheduleSummary, httpDescription, eventDescription, dateLabel, exportURL, passwordError, filterLibrary, selectTopics } from './helpers.js';

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
