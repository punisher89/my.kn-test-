// New menus and compact editors use only synthetic local data.
const fs=require('fs'),path=require('path'),assert=require('node:assert/strict');
const {chromium}=require('playwright');const root=path.resolve(__dirname,'..');
const read=p=>fs.readFileSync(path.join(root,p));
const demo=JSON.parse(read('design/demo-state.json'));demo.service_presets=JSON.parse(read('internal/pilot/service-presets.json'));
let update={supported:true,current:'0.2.0-alpha.10',engine:'v1.0.7-z2k-r0',stage:'idle',running:false,available:false};let installs=0,deviceReads=0,syncs=0,creates=0,deviceNull=true,checkRequests=0,checkGate=null;
demo.checks={};
(async()=>{const browser=await chromium.launch({headless:true,executablePath:process.env.PILOT_CHROMIUM});try{
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.route('http://pilot.test/**',async route=>{
  const req=route.request(),url=new URL(req.url()),json=(b,status=200)=>route.fulfill({json:b,status});
  if(url.pathname==='/auth/status')return json({configured:true,authenticated:true});
  if(url.pathname==='/api/status')return json(demo);
  if(url.pathname==='/api/device'){deviceReads++;return json({model:'KN-1811 · пример',platform:'linux/arm64',kernel:'test',cpu_cores:4,cpu_sample_available:true,cpu_percent:deviceNull?null:12.5,load:[0.2,0.1,0.1],memory_total:268435456,memory_available:134217728,uptime_seconds:90000,temperatures:[],wan:'ppp0',rx_bytes:1000,tx_bytes:500,rx_bytes_per_second:null,tx_bytes_per_second:null});}
  if(url.pathname==='/api/adaptive')return json(JSON.parse(read('design/demo-adaptive.json')));
  if(url.pathname==='/api/check'){checkRequests++;const id=req.postDataJSON().profile,p=demo.config.profiles.find(p=>p.id===id);if(checkGate)await new Promise(resolve=>{checkGate.resolve=resolve;});const results=[{url:p.targets[0].url,tls:'1.2',family:'tcp4',ok:true,status:200,duration_ms:145,attempt:1},{url:p.targets[0].url,tls:'1.3',family:'tcp4',ok:false,status:451,duration_ms:160,error:'HTTP 451 <img src=x onerror=alert(1)>',attempt:1}];demo.checks[id]={profile:id,started:new Date().toISOString(),finished:new Date().toISOString(),strategy:p.strategy,adaptive:false,running:true,complete:true,passed:false,stale:false,results};return json(results);}
  if(url.pathname==='/api/updates')return json(update);
  if(url.pathname==='/api/updates/check'){update={...update,latest:'0.2.0-alpha.11',latest_engine:'v-next',available:true};return json(update);}
  if(url.pathname==='/api/updates/install'){assert.deepEqual(req.postDataJSON(),{version:'0.2.0-alpha.11'});installs++;update={...update,running:true,stage:'downloading'};return json(update,202);}
  if(url.pathname==='/api/services/custom'){creates++;const p=req.postDataJSON();demo.config.profiles.push(p);return json(demo);}
  if(url.pathname.endsWith('/domains')){syncs++;const id=url.pathname.split('/')[3],p=demo.config.profiles.find(p=>p.id===id);if(!p.domains.includes('static.customcdn.test'))p.domains.push('static.customcdn.test');p.domains_updated=new Date().toISOString();return json(demo);}
  if(url.pathname==='/api/config'){demo.config=req.postDataJSON();return json(demo);}
  if(url.pathname==='/api/services'){const s=demo.service_presets.find(s=>s.profile.id===req.postDataJSON().id);demo.config.profiles.push(structuredClone(s.profile));return json(demo);}
  const file=url.pathname==='/'?'index.html':url.pathname.slice(1);if(!['index.html','app.js','style.css','setup.css','favicon.svg'].includes(file))return route.abort();
  return route.fulfill({body:read('internal/pilot/web/'+file),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html'});
 });
 await page.goto('http://pilot.test/#device');await page.locator('#device-data h3').first().waitFor();
 assert.match(await page.locator('#device-data').innerText(),/ожидается второй замер/);
 assert.match(await page.locator('#device-data').innerText(),/Недоступно: прошивка/);
 deviceNull=false;await page.evaluate(()=>loadDevice());assert.match(await page.locator('#device-data').innerText(),/12.5 %/);
 assert.doesNotMatch(await page.locator('#device-data').innerText(),/0.0 °C/);
 await page.evaluate(()=>{location.hash='updates';});await page.locator('#update-check').waitFor();await page.waitForFunction(()=>updatesData!==null);
 await page.locator('#update-check').click();await page.waitForFunction(()=>updatesData.available);assert.equal(installs,0);
 await page.locator('#update-install').click();await page.waitForFunction(()=>updatesData.running);assert.equal(installs,1);assert.equal(await page.locator('#update-install').isDisabled(),true);
 update={...update,current:'0.2.0-alpha.11',running:false,stage:'completed',available:false};await page.evaluate(()=>loadUpdates());assert.match(await page.locator('#update-info').innerText(),/Обновление завершено/);
 // The approved Matrix overview uses real-shaped telemetry, explicit checks,
 // escaped errors, a bounded chart and mobile layout rather than fake successes.
 await page.evaluate(()=>{location.hash='overview';});await page.locator('#network-telemetry .telemetry-cell').first().waitFor();
 assert.match(await page.locator('#content h1').innerText(),/Состояние сети/);
 assert.equal(await page.locator('.telemetry-cell').count(),4);
 assert.equal(await page.locator('.matrix-rain').count(),2);
 assert.equal(await page.locator('.matrix-rain span').count(),8);
 assert.equal(await page.locator('.matrix-rain').first().evaluate(el=>getComputedStyle(el).pointerEvents),'none');
 assert.equal(await page.locator('.matrix-rain span').first().evaluate(el=>getComputedStyle(el).animationName),'matrix-fall');
 await page.emulateMedia({reducedMotion:'reduce'});assert.equal(await page.locator('.matrix-rain').first().isVisible(),false);await page.emulateMedia({reducedMotion:'no-preference'});
 await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'));});
 assert.equal(await page.locator('.matrix-rain span').first().evaluate(el=>getComputedStyle(el).animationPlayState),'paused');
 await page.evaluate(()=>{delete document.hidden;document.dispatchEvent(new Event('visibilitychange'));});

 assert.equal(await page.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--accent').trim()),'#57f68b');
 const originalConfig=JSON.stringify(demo.config);checkGate={};await page.locator('[data-check-profile="youtube"]').click();
 await page.waitForFunction(()=>checkPending.has('youtube'));assert.equal(await page.locator('[data-check-profile="discord"]').isDisabled(),true);
 while(!checkGate.resolve)await new Promise(resolve=>setTimeout(resolve,10));checkGate.resolve();checkGate=null;
 await page.waitForFunction(()=>!checkPending.size&&state.checks?.youtube);
 assert.equal(checkRequests,1);assert.equal(JSON.stringify(demo.config),originalConfig,'Checking must not modify strategies or config');
 assert.match(await page.locator('[data-check-status="youtube"]').innerText(),/Есть ошибки/);
 await page.locator('[data-check-view="youtube"] > summary').click();await page.evaluate(()=>refresh());
 assert.equal(await page.locator('[data-check-view="youtube"]').evaluate(el=>el.open),true);
 assert.match(await page.locator('[data-check-report="youtube"]').innerText(),/HTTP 451 <img/);
 assert.equal(await page.locator('[data-check-report="youtube"] img').count(),0,'Network error must be text, never HTML');
 assert.match(await page.locator('[data-check-report="youtube"]').innerText(),/без отдельной HTTPS-пробы/);
 demo.checks.youtube.stale=true;await page.evaluate(()=>refresh());assert.match(await page.locator('[data-check-status="youtube"]').innerText(),/устарела/);
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Matrix overview fits desktop');
 await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Matrix overview fits mobile');await page.setViewportSize({width:1280,height:900});
 await page.evaluate(()=>{location.hash='profiles';});await page.locator('.custom-service').waitFor();await page.locator('.custom-service > summary').click();
 const form=page.locator('#custom-service-form');await form.locator('[name=name]').fill('Мой сервис');await form.locator('[name=url]').fill('https://custom.example.com/');await form.locator('[name=contains]').fill('Expected control text');
 await form.locator('details > summary').click();await form.locator('[name=source]').fill('https://lists.example.com/service.txt');await form.locator('button[type=submit]').click();await page.waitForFunction(()=>!serviceMutationPending);
 assert.equal(creates,1);assert.equal(syncs,1);const custom=demo.config.profiles.at(-1);assert.equal(custom.name,'Мой сервис');assert.ok(custom.auto_domains);assert.ok(custom.domains.includes('static.customcdn.test'));assert.ok(custom.domains.includes('custom.example.com'));
 const card=page.locator(`[data-profile-card="${custom.id}"]`);await card.locator('summary').first().click();await card.locator('.domain-source-editor > summary').click();
 assert.equal(await page.locator(`#source-mode-${custom.id}`).inputValue(),'url');assert.equal(await page.locator(`#source-url-${custom.id}`).inputValue(),'https://lists.example.com/service.txt');
 await card.locator(`[data-domain-sync="${custom.id}"]`).click();await page.waitForFunction(()=>!serviceMutationPending);assert.equal(syncs,2);
 await page.locator('#service-catalog > summary').click();await page.locator('#service-search').fill('телеграм');await page.locator('[data-add-service="telegram-web"]').click();await page.waitForFunction(()=>!serviceMutationPending);
 assert.ok(demo.config.profiles.some(p=>p.id==='telegram-web'));assert.match(demo.service_presets.find(p=>p.profile.id==='telegram-web').note,/MTProto/);
 // Detailed trial rows stay closed and retain expansion across refreshes.
 demo.job={profile:custom.id,started:new Date().toISOString(),finished:new Date().toISOString(),stage:'done',selected:'split-sni',trials:Array.from({length:12},(_,i)=>({strategy:i===0?'direct':'split-sni',passed:i===11,results:[{url:'https://custom.example.com/',tls:'1.3',family:'tcp4',attempt:1,ok:i===11,status:200,duration_ms:100,error:i===11?'':'Connection failed'}]}))};
 await page.evaluate(()=>{location.hash='tune';});await page.locator('[data-trial]').first().waitFor();await page.evaluate(()=>refresh());assert.equal(await page.locator('[data-trial]').count(),12);assert.equal(await page.locator('[data-trial][open]').count(),0);
 await page.locator('[data-trial]').first().locator('summary').click();await page.evaluate(()=>refresh());assert.equal(await page.locator('[data-trial]').first().evaluate(el=>el.open),true);
 await page.evaluate(()=>{location.hash='strategies';});await page.locator('#method-search').waitFor();assert.equal(await page.locator('.method-family[open]').count(),0);
 await page.locator('#method-search').fill('hostfake');assert.ok(await page.locator('.method-family').count()>0);assert.ok(await page.locator('.method-family[open]').count()>0);
 const item=page.locator('.method-item').first();await item.locator('summary').click();assert.equal(await item.evaluate(el=>el.open),true);
 await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
 for(const hash of ['overview','device','updates','profiles','tune','settings','adaptive']){await page.evaluate(h=>{location.hash=h;},hash);await page.waitForFunction(h=>page===h,hash);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,hash+' must fit mobile width');}
 await page.evaluate(()=>{location.hash='settings';});await page.locator('#adaptive_fast_start').waitFor();
 assert.equal(await page.locator('#adaptive_fast_start').isChecked(),true);
 const limits=[demo.config.adaptive_cooldown_seconds,demo.config.adaptive_max_switches,demo.config.adaptive_global_max_switches];
 await page.locator('#adaptive_fast_start').uncheck();await page.locator('#settings-form button[type=submit]').click();await page.waitForFunction(()=>state.config.adaptive_fast_start===false);
 assert.deepEqual([demo.config.adaptive_cooldown_seconds,demo.config.adaptive_max_switches,demo.config.adaptive_global_max_switches],limits,'Fast-start opt-out preserves configured limits');
 assert.deepEqual(errors,[]);console.log('PASS: router telemetry, missing temperature, signed-update UI flow, custom services, domain sources, Telegram Web, compact trials/methods, preserved expansion, mobile menus, Matrix overview, read-only service reports, safe error rendering, animated rain, reduced motion, hidden-tab pause, fast-start opt-out');
}finally{await browser.close();}})().catch(e=>{console.error(e);process.exitCode=1});
