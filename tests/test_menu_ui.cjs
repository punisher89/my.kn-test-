// New menus and compact editors use only synthetic local data.
const fs=require('fs'),path=require('path'),assert=require('node:assert/strict');
const {chromium}=require('playwright');const root=path.resolve(__dirname,'..');
const read=p=>fs.readFileSync(path.join(root,p));
const demo=JSON.parse(read('design/demo-state.json'));demo.service_presets=JSON.parse(read('internal/pilot/service-presets.json'));
let update={supported:true,current:'0.2.0-alpha.10',engine:'v1.0.7-z2k-r0',stage:'idle',running:false,available:false};let installs=0,deviceReads=0,syncs=0,creates=0;
(async()=>{const browser=await chromium.launch({headless:true,executablePath:process.env.PILOT_CHROMIUM});try{
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.route('http://pilot.test/**',async route=>{
  const req=route.request(),url=new URL(req.url()),json=(b,status=200)=>route.fulfill({json:b,status});
  if(url.pathname==='/auth/status')return json({configured:true,authenticated:true});
  if(url.pathname==='/api/status')return json(demo);
  if(url.pathname==='/api/device'){deviceReads++;return json({model:'KN-1811 · пример',platform:'linux/arm64',kernel:'test',cpu_cores:4,cpu_sample_available:true,cpu_percent:deviceReads>1?12.5:null,load:[0.2,0.1,0.1],memory_total:268435456,memory_available:134217728,uptime_seconds:90000,temperatures:[],wan:'ppp0',rx_bytes:1000,tx_bytes:500,rx_bytes_per_second:null,tx_bytes_per_second:null});}
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
 await page.evaluate(()=>loadDevice());assert.match(await page.locator('#device-data').innerText(),/12.5 %/);
 assert.doesNotMatch(await page.locator('#device-data').innerText(),/0.0 °C/);
 await page.evaluate(()=>{location.hash='updates';});await page.locator('#update-check').waitFor();await page.waitForFunction(()=>updatesData!==null);
 await page.locator('#update-check').click();await page.waitForFunction(()=>updatesData.available);assert.equal(installs,0);
 await page.locator('#update-install').click();await page.waitForFunction(()=>updatesData.running);assert.equal(installs,1);assert.equal(await page.locator('#update-install').isDisabled(),true);
 update={...update,current:'0.2.0-alpha.11',running:false,stage:'completed',available:false};await page.evaluate(()=>loadUpdates());assert.match(await page.locator('#update-info').innerText(),/Обновление завершено/);
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
 for(const hash of ['device','updates','profiles','tune']){await page.evaluate(h=>{location.hash=h;},hash);await page.waitForFunction(h=>page===h,hash);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,hash+' must fit mobile width');}
 assert.deepEqual(errors,[]);console.log('PASS: router telemetry, missing temperature, signed-update UI flow, custom services, domain sources, Telegram Web, compact trials/methods, preserved expansion, mobile menus');
}finally{await browser.close();}})().catch(e=>{console.error(e);process.exitCode=1});
