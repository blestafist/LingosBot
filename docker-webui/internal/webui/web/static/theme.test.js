import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const script = readFileSync(new URL('./theme.js', import.meta.url), 'utf8');
test('appearance is applied before rendering, validated, persisted and works without storage', () => {
  for (const stored of ['dark', 'light', 'system', 'invalid', null, 'blocked']) {
    const root = { dataset: {} };
    let ready, change, saved;
    const select = { addEventListener: (_, listener) => { change = listener; } };
    vm.runInNewContext(script, {
      document: { documentElement: root, addEventListener: (_, listener) => { ready = listener; }, getElementById: () => select },
      localStorage: {
        getItem: () => { if (stored === 'blocked') throw new Error('denied'); return stored; },
        setItem: (key, value) => { if (stored === 'blocked') throw new Error('denied'); saved = [key, value]; },
      },
    });
    assert.equal(root.dataset.theme, ['dark', 'light'].includes(stored) ? stored : 'system');
    ready();
    assert.equal(select.value, root.dataset.theme);
    select.value = 'dark'; change();
    assert.equal(root.dataset.theme, 'dark');
    if (stored !== 'blocked') assert.deepEqual(saved, ['lingos-theme', 'dark']);
  }
});
