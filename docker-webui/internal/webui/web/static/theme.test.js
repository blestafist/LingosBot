import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const script = readFileSync(new URL('./theme.js', import.meta.url), 'utf8');

function loadTheme({ dark = false, stored = null, storage = 'available', hasButton = true } = {}) {
  const root = { dataset: {} };
  const saved = [];
  let ready, click, mediaChange;
  const button = {
    attributes: {},
    setAttribute(name, value) { this.attributes[name] = value; },
    addEventListener(event, listener) { assert.equal(event, 'click'); click = listener; },
  };
  const media = {
    matches: dark,
    addEventListener(event, listener) { assert.equal(event, 'change'); mediaChange = listener; },
  };
  const context = {
    document: {
      documentElement: root,
      addEventListener(event, listener) { assert.equal(event, 'DOMContentLoaded'); ready = listener; },
      getElementById(id) { assert.equal(id, 'theme-toggle'); return hasButton ? button : null; },
    },
    window: { matchMedia(query) { assert.equal(query, '(prefers-color-scheme: dark)'); return media; } },
  };
  if (storage !== 'missing') {
    context.localStorage = {
      getItem(key) {
        assert.equal(key, 'lingos-theme');
        if (storage === 'blocked') throw new Error('denied');
        return stored;
      },
      setItem(key, value) {
        if (storage === 'blocked') throw new Error('denied');
        saved.push([key, value]);
      },
    };
  }
  vm.runInNewContext(script, context);
  return {
    root, button, saved,
    ready: () => ready(),
    click: () => click(),
    changeSystem(dark) { media.matches = dark; mediaChange({ matches: dark }); },
  };
}

function assertTheme(page, theme) {
  assert.equal(page.root.dataset.theme, theme);
  const label = `Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`;
  assert.equal(page.button.attributes['aria-label'], label);
  assert.equal(page.button.attributes.title, label);
  assert.equal(page.button.attributes['aria-pressed'], undefined);
}

for (const dark of [false, true]) {
  const system = dark ? 'dark' : 'light';
  const opposite = dark ? 'light' : 'dark';

  test(`${system} OS: applies validated preference before paint and follows system only by default`, () => {
    for (const stored of [null, 'system', 'invalid', 'light', 'dark']) {
      const explicit = stored === 'light' || stored === 'dark';
      const page = loadTheme({ dark, stored });
      assert.equal(page.root.dataset.theme, explicit ? stored : system);
      page.ready();
      assertTheme(page, explicit ? stored : system);
      page.changeSystem(!dark);
      assertTheme(page, explicit ? stored : opposite);
      assert.deepEqual(page.saved, []);
    }
  });

  test(`${system} OS: toggles twice, persists each choice and ignores later system changes`, () => {
    const page = loadTheme({ dark });
    page.ready();
    page.click();
    assertTheme(page, opposite);
    assert.deepEqual(page.saved, [['lingos-theme', opposite]]);

    const reloaded = loadTheme({ dark, stored: page.saved[0][1] });
    assert.equal(reloaded.root.dataset.theme, opposite);
    reloaded.ready();
    assertTheme(reloaded, opposite);

    page.changeSystem(!dark);
    page.changeSystem(dark);
    assertTheme(page, opposite);
    page.click();
    assertTheme(page, system);
    assert.deepEqual(page.saved, [['lingos-theme', opposite], ['lingos-theme', system]]);
    page.changeSystem(!dark);
    assertTheme(page, system);
  });

  test(`${system} OS: handles system changes before the button is initialized`, () => {
    const page = loadTheme({ dark });
    page.changeSystem(!dark);
    assert.equal(page.root.dataset.theme, opposite);
    page.ready();
    assertTheme(page, opposite);
    page.click();
    assertTheme(page, system);
    assert.deepEqual(page.saved, [['lingos-theme', system]]);
  });

  test(`${system} OS: toggles safely with blocked or missing storage`, () => {
    for (const storage of ['blocked', 'missing']) {
      const page = loadTheme({ dark, storage });
      assert.equal(page.root.dataset.theme, system);
      page.ready();
      assertTheme(page, system);
      page.changeSystem(!dark);
      assertTheme(page, opposite);
      page.click();
      assertTheme(page, system);
      page.changeSystem(dark);
      page.changeSystem(!dark);
      assertTheme(page, system);
      page.click();
      assertTheme(page, opposite);
      assert.deepEqual(page.saved, []);
    }
  });
}

test('system theme still applies on pages without a toggle button or storage', () => {
  for (const dark of [false, true]) {
    const page = loadTheme({ dark, hasButton: false, storage: 'missing' });
    assert.equal(page.root.dataset.theme, dark ? 'dark' : 'light');
    page.ready();
    page.changeSystem(!dark);
    assert.equal(page.root.dataset.theme, dark ? 'light' : 'dark');
  }
});
