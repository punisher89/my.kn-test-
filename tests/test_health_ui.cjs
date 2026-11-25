// Exercise the overview with a delayed router, a failed status read and a cancellable batch.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {chromium} = require('playwright');
const root = path.resolve(__dirname, '..');
const read = name => fs.readFileSync(path.join(root, name));
const demo = JSON.parse(read('design/demo-state.json'));
const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return {promise, resolve};
};

(async () => {
  const browser = await chromium.launch({headless: true, executablePath: process.env.PILOT_CHROMIUM});
  try {
    const page = await browser.newPage();
    const errors = [], requests = [];
    let statusReads = 0, active = 0, maxActive = 0, rejectStatus = false;
    let statusGate, checkGate, cooldown = true;
    const deviceGate = deferred();
    page.on('pageerror', err => errors.push(err.message));
    await page.route('http://pilot.test/**', async route => {
      const url = new URL(route.request().url());
      if (url.pathname === '/auth/status') return route.fulfill({json: {configured:true, authenticated:true}});
      if (url.pathname === '/api/status') {
        statusReads++;
        const data = structuredClone(demo);
        if (statusGate) {
          const gate = statusGate;
          statusGate = null;
          gate.started.resolve();
          await gate.release.promise;
        }
        return rejectStatus ? route.fulfill({status:503, body:'Unavailable'}) : route.fulfill({json:data});
      }
      if (url.pathname === '/api/device') {
        await deviceGate.promise;
        return route.fulfill({json:{cpu_percent:13, load:[], temperatures:[], wan:'ppp0'}});
      }
      if (url.pathname === '/api/adaptive') return route.fulfill({json:{boxes:[]}});
      if (url.pathname === '/api/check') {
        const {profile} = route.request().postDataJSON();
        requests.push(profile);
        active++;
        maxActive = Math.max(maxActive, active);
        try {
          if (checkGate) {
            const gate = checkGate;
            checkGate = null;
            gate.started.resolve();
            await gate.release.promise;
          }
          return await (cooldown && profile === 'youtube'
            ? route.fulfill({status:429,json:{error:'Проверено недавно'}}) : route.fulfill({json:[]}));
        } finally { active--; }
      }
      if (url.pathname === '/api/telegram/relay-draft' || url.pathname === '/api/preflight') return route.fulfill({json:{}});
      const file = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      const types = {'html':'text/html','js':'text/javascript','css':'text/css','svg':'image/svg+xml'};
      if (!Object.hasOwn(types,path.extname(file).slice(1))) return route.abort();
      return route.fulfill({body:read('internal/pilot/web/'+file),contentType:types[path.extname(file).slice(1)]});
    });
    await page.goto('http://pilot.test/#overview');
    await page.locator('#check-all').waitFor();
    // Drive polling explicitly so the test does not depend on wall-clock ticks.
    await page.evaluate(() => { clearTimeout(pollTimer); });
    await page.evaluate(() => refresh());
    deviceGate.resolve();
    await page.waitForFunction(() => document.querySelector('#network-telemetry').textContent.includes('13.0'));

    const gate = {started:deferred(), release:deferred()};
    statusGate = gate;
    const before = statusReads;
    await page.evaluate(() => { window.slowPoll = pollStatus(); });
    await gate.started.promise;
    await page.evaluate(() => pollStatus());
    assert.equal(statusReads,before+1,'overlapping polls');
    demo.version = 'newer-status';
    await page.evaluate(() => refresh());
    gate.release.resolve();
    await page.evaluate(() => window.slowPoll);
    assert.equal(await page.locator('#version').innerText(),'vnewer-status','older response replaced latest state');
    rejectStatus = true;
    await page.evaluate(() => pollStatus());
    assert.match(await page.locator('#connection-banner').innerText(),/Последние данные получены/);
    assert.equal(await page.locator('#check-all').isDisabled(),true);
    rejectStatus = false;
    await page.locator('#retry-connection').click();
    await page.locator('#connection-banner').waitFor({state:'hidden'});

    await page.locator('#check-all').click();
    await page.waitForFunction(() => document.querySelector('#batch-status').textContent.includes('Недавно проверены: 1'));
    assert.deepEqual(requests,['youtube','discord']);
    assert.equal(maxActive,1);
    cooldown = false;
    const hold = {started:deferred(),release:deferred()};
    checkGate = hold;
    await page.locator('#check-all').click();
    await hold.started.promise;
    await page.locator('#check-stop').click();
    await page.waitForFunction(() => document.querySelector('#batch-status').textContent.includes('остановлена'));
    hold.release.resolve();
    assert.deepEqual(requests,['youtube','discord','youtube'],'cancellation started another profile');
    demo.last_video_check = {finished:'2026-11-20T20:00:00Z',ok:true,status:206,bytes:65536,duration_ms:25};
    await page.evaluate(() => refresh());
    const leave = {started:deferred(),release:deferred()};
    checkGate = leave;
    await page.locator('#check-all').click();
    await leave.started.promise;
    await page.evaluate(() => { location.hash = 'profiles'; });
    await page.waitForFunction(() => !batch);
    leave.release.resolve();
    assert.deepEqual(requests,['youtube','discord','youtube','youtube'],'navigation left the queue running');
    await page.locator('#youtube-video-report').waitFor();
    assert.match(await page.locator('#youtube-video-report').innerText(),/Последний замер.*206.*65536/);
    await page.evaluate(() => { location.hash = 'device'; });
    assert.equal(await page.locator('.panel-address a').innerText(), 'http://pilot.test');
    assert.deepEqual(errors,[]);
    console.log('PASS: delayed telemetry, stale replies, single poll, reconnection, sequential checks, cooldown, cancellation, video history and panel address');
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
