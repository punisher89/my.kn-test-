// Browser regression checks using synthetic observations; no router is contacted.
// Requires Playwright. Set PILOT_CHROMIUM for a system Chromium executable.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');
const root = path.resolve(__dirname, '..');
const read = (name) => fs.readFileSync(path.join(root, name));
const demo = JSON.parse(read('design/demo-state.json'));
const snapshot = JSON.parse(read('design/demo-adaptive.json'));
const firstID = snapshot.boxes[0].id;
let calls = 0, fail = false, holdResponse;
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.PILOT_CHROMIUM });
  try {
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.clock.install();
    await page.route('http://pilot.test/**', async (route) => {
      const url = new URL(route.request().url());
      const json = (body, status = 200) => route.fulfill({ status, json: body });
      if (url.pathname === '/auth/status') return json({ configured: true, authenticated: true });
      if (url.pathname === '/api/status') return json(demo);
      if (url.pathname === '/api/adaptive') {
        calls++;
        if (holdResponse) await new Promise((resolve) => { holdResponse.resolve = resolve; });
        return fail ? json({error:'Проверка ошибки обновления'}, 503) : json(snapshot);
      }
      const file = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      if (!['index.html', 'style.css', 'setup.css', 'app.js', 'favicon.svg'].includes(file)) return route.abort();
      return route.fulfill({ body: read('internal/pilot/web/' + file), contentType: file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : file.endsWith('.svg') ? 'image/svg+xml' : 'text/html' });
    });
    await page.goto('http://pilot.test/#adaptive');
    const cards = page.locator('[data-box]');
    await cards.first().waitFor();
    assert.equal(await cards.count(), 2);
    const search = page.locator('#adaptive-search'), filter = page.locator('#adaptive-filter');
    await search.fill('EXAMPLE.COM');
    assert.equal(await cards.count(), 1);
    await filter.selectOption('connected');
    assert.equal(await cards.count(), 0, 'Search and status must match the same host');
    await search.fill('googlevideo.com');
    assert.equal(await cards.count(), 1);
    await search.fill('');
    await filter.selectOption('all');
    const card = page.locator(`[data-box="${firstID}"]`);
    await card.locator('details').first().evaluate((el) => { el.open = true; });
    const management = card.locator('.domain-management');
    await management.evaluate((el) => { el.open = true; });
    const select = management.locator('select').first();
    const options = await select.locator('option').evaluateAll((items) => items.map((el) => el.value));
    const draft = options.find((value) => value !== 'fake-md5');
    await select.focus();
    await select.selectOption(draft);
    snapshot.boxes[0].hosts[0].status = 'connected';
    // Background refresh must keep the native control itself attached and focused.
    await select.evaluate((el) => { window.editingSelect = el; });
    await page.evaluate(() => refresh());
    assert.equal(await page.evaluate(() => document.activeElement === window.editingSelect && window.editingSelect.isConnected), true);
    assert.equal(await select.inputValue(), draft);
    assert.match(await page.locator('#adaptive-update').innerText(), /после выхода/);
    await search.focus();
    await page.clock.runFor(1);
    assert.equal(await select.inputValue(), draft);
    assert.equal(await management.evaluate((el) => el.open), true);
    assert.equal(await card.locator('details').first().evaluate((el) => el.open), true);
    // Hiding and restoring the box must retain the draft and expanded sections.
    await search.fill('not-present.invalid');
    assert.equal(await cards.count(), 0);
    await search.fill('');
    assert.equal(await select.inputValue(), draft);
    assert.equal(await management.evaluate((el) => el.open), true);
    // Exercise the actual four-second interval and preserve the filter.
    await filter.selectOption('probing');
    const before = calls;
    const receivedBefore = await page.evaluate(() => adaptiveData.receivedAt);
    await page.clock.runFor(4100);
    await page.waitForFunction((before) => adaptiveData.receivedAt > before, receivedBefore);
    assert.ok(calls > before);
    assert.equal(await filter.inputValue(), 'probing');
    await filter.selectOption('all');
    // Error leaves the latest data visible and is explained near the controls.
    fail = true;
    await page.evaluate(() => loadAdaptive());
    assert.equal(await cards.count(), 2);
    assert.match(await page.locator('#adaptive-update').innerText(), /Не удалось обновить/);
    fail = false;
    // Future, expired, pinned, and paused holds have different effective filters.
    const row = snapshot.boxes[0].hosts[0];
    row.status = 'probing'; row.hold_reason = 'limit';
    row.hold_until = await page.evaluate(() => Date.now() / 1000 + 60);
    await page.evaluate(() => loadAdaptive());
    await filter.selectOption('held');
    assert.equal(await cards.count(), 1);
    assert.match(await cards.first().innerText(), /Лимит смен за 10 минут/);
    row.pinned = row.strategy;
    await page.evaluate(() => loadAdaptive());
    assert.equal(await cards.count(), 0);
    await filter.selectOption('pinned');
    assert.equal(await cards.count(), 1);
    row.paused = true;
    await page.evaluate(() => loadAdaptive());
    await filter.selectOption('paused');
    assert.equal(await cards.count(), 1);
    row.paused = false; row.pinned = ''; row.hold_until = 1;
    await page.evaluate(() => loadAdaptive());
    await filter.selectOption('held');
    assert.equal(await cards.count(), 0);
    await filter.selectOption('all');
    // Poll responses arriving after navigation must not overwrite a new view.
    holdResponse = {};
    const pending = page.evaluate(() => loadAdaptive());
    await page.waitForFunction(() => adaptiveLoading);
    await page.evaluate(() => { location.hash = 'overview'; });
    await page.locator('#adaptive-results').waitFor({ state: 'detached' });
    await page.waitForFunction(() => location.hash === '#overview');
    // Wait for route capture without advancing the application's polling clock.
    while (!holdResponse.resolve) await new Promise((resolve) => setTimeout(resolve, 10));
    holdResponse.resolve(); holdResponse = null;
    await pending;
    await page.evaluate(() => { location.hash = 'adaptive'; });
    await cards.first().waitFor();
    assert.equal(await select.inputValue(), draft);
    assert.equal(await management.evaluate((el) => el.open), true);
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'Mobile view must fit the viewport');
    assert.deepEqual(errors, []);
    console.log('PASS: search, effective filters, polling, drafts, focus, expanded sections, errors, navigation races, mobile layout');
  } finally { await browser.close(); }
})().catch((error) => { console.error(error); process.exitCode = 1; });
