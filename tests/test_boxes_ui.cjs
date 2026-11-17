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
let calls = 0, fail = false, diagnosticFail = false, holdResponse;
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.PILOT_CHROMIUM });
  try {
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.clock.install();
    const freshAt = await page.evaluate(() => Math.floor(Date.now() / 1000) - 30);
    for (const r of [...snapshot.hosts, ...snapshot.history, ...snapshot.boxes.flatMap(b => [...b.hosts, ...b.history])]) r.updated = freshAt;
    await page.route('http://pilot.test/**', async (route) => {
      const url = new URL(route.request().url());
      const json = (body, status = 200) => route.fulfill({ status, json: body });
      if (url.pathname === '/auth/status') return json({ configured: true, authenticated: true });
      if (url.pathname === '/api/status') return json(demo);
      if (url.pathname === '/api/diagnostics') {
        if (diagnosticFail) return json({error:'Ошибка тестового экспорта'}, 503);
        return json({schema_version:1, generated_at:new Date().toISOString(), service:{mode:'mockup'}, current:snapshot.boxes.flatMap(b=>b.hosts), notes:['Synthetic test report, no router connection']});
      }
      if (url.pathname === '/api/adaptive') {
        calls++;
        if (holdResponse && !holdResponse.captured) { const held = holdResponse; held.captured = true; await new Promise((resolve) => { held.resolve = resolve; }); }
        return fail ? json({error:'Проверка ошибки обновления'}, 503) : json(snapshot);
      }
      const file = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      if (!['index.html', 'style.css', 'setup.css', 'app.js','music.js', 'favicon.svg'].includes(file)) return route.abort();
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
    const downloaded = page.waitForEvent('download');
    await page.locator('#adaptive-download').click();
    const download = await downloaded;
    assert.match(download.suggestedFilename(), /^keenetic-pilot-diagnostics-.*\.json$/);
    const chunks = [];
    for await (const chunk of await download.createReadStream()) chunks.push(chunk);
    const report = JSON.parse(Buffer.concat(chunks).toString());
    assert.equal(report.schema_version, 1);
    assert.equal(report.current.length, snapshot.boxes.flatMap(b=>b.hosts).length, 'Export is independent of current filters');
    assert.equal(await page.locator('#adaptive-download').isEnabled(), true);
    diagnosticFail = true;
    let downloads = 0;
    const countDownload = () => downloads++;
    page.on('download', countDownload);
    await page.locator('#adaptive-download').click();
    await page.waitForFunction(() => !document.querySelector('#adaptive-download').disabled);
    assert.match(await page.locator('#toast').innerText(), /Не удалось скачать диагностику/);
    assert.equal(downloads, 0, 'Failed requests must not produce a report');
    page.off('download', countDownload);
    diagnosticFail = false;
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
    assert.match(await cards.first().innerText(), /Лимит смен домена или движка за 10 минут/);
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

    // A stale record retains its method and moves to a collapsed saved section.
    const oldRow = {...snapshot.boxes[0].hosts[0], host:'old.example.com', updated:await page.evaluate(() => Date.now()/1000 - 86400), pinned:'', paused:false, hold_until:0};
    const oldMethod = oldRow.strategy;
    const oldBox = {...snapshot.boxes[0], id:'box-saved-test', hosts:[oldRow], history:[], methods:[]};
    snapshot.boxes.push(oldBox);
    await page.evaluate(() => loadAdaptive());
    const saved = page.locator('.saved-boxes');
    assert.equal(await saved.evaluate(el => el.open), false);
    await filter.selectOption('stale');
    assert.equal(await saved.evaluate(el => el.open), true, 'Filters reveal saved matches');
    assert.equal(await cards.count(), 1);
    assert.match(await cards.first().innerText(), /Нет свежих наблюдений/);
    assert.equal(oldRow.strategy, oldMethod);
    await filter.selectOption('probing');
    assert.equal(await page.locator('[data-box="box-saved-test"]').count(), 0);
    await filter.selectOption('all');
    // A new observation returns the same method/context to the recent section.
    oldRow.updated = await page.evaluate(() => Date.now()/1000);
    oldRow.status = 'connected';
    await page.evaluate(() => loadAdaptive());
    assert.equal(await page.locator('.saved-boxes [data-box="box-saved-test"]').count(), 0);
    assert.match(await page.locator('[data-box="box-saved-test"]').innerText(), /Есть ответ/);
    assert.equal(oldRow.strategy, oldMethod);
    const legacyRow = {...oldRow, host:'legacy.example.com', network:'-', port:0};
    snapshot.boxes.push({...oldBox, id:'box-legacy-test', network:'-', port:0, hosts:[legacyRow]});
    await page.evaluate(() => loadAdaptive());
    assert.equal(await page.locator('.saved-boxes [data-box="box-legacy-test"]').count(), 1);
    // A saved match appearing after an empty search is revealed on the next poll.
    await search.fill('late.example.com');
    assert.equal(await cards.count(), 0);
    const lateRow = {...legacyRow, host:'late.example.com'};
    snapshot.boxes.push({...oldBox, id:'box-late-test', network:'-', port:0, hosts:[lateRow]});
    await page.evaluate(() => loadAdaptive());
    assert.equal(await saved.evaluate(el => el.open), true);
    assert.equal(await page.locator('[data-box="box-late-test"]').isVisible(), true);
    await search.fill('');
    // Interleaving other hosts does not hide a transition or rewrite raw history.
    const grouping = await page.evaluate(() => {
      const a={host:'a.example.com', group:'g', network:'ppp0', port:443, strategy:'split-sni', status:'failed', symptom:'rst', origin:'pool', updated:100};
      const raw=[a,{...a,host:'b.example.com',updated:101},{...a,updated:102},{...a,status:'selected',updated:103},{...a,updated:104}];
      const before=JSON.stringify(raw), grouped=groupedHistory(raw);
      const counted=groupedHistory([{...a,event_count:4,first_updated:90},{...a,updated:105,event_count:3,first_updated:103}])[0];
      return {storedCount:counted.event_count,storedFirst:counted.first_updated,count:grouped.length, counts:grouped.map(r=>r.event_count), first:grouped[2].first_updated, last:grouped[2].updated, unchanged:before===JSON.stringify(raw), boundary:observationStale({updated:100},86500), beforeBoundary:observationStale({updated:101},86500)};
    });
    assert.equal(grouping.storedCount,7); assert.equal(grouping.storedFirst,90);
    assert.equal(grouping.count,4);
    assert.deepEqual(grouping.counts,[1,1,2,1]);
    assert.equal(grouping.first,100); assert.equal(grouping.last,102);
    assert.equal(grouping.unchanged,true);
    assert.equal(grouping.boundary,true); assert.equal(grouping.beforeBoundary,false);
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'Mobile view must fit the viewport');
    assert.deepEqual(errors, []);
    console.log('PASS: search, effective filters, polling, drafts, focus, expanded sections, errors, navigation races, diagnostic downloads and failures, freshness, saved records, history grouping, mobile layout');
  } finally { await browser.close(); }
})().catch((error) => { console.error(error); process.exitCode = 1; });
