// New menus and compact editors use only synthetic local data.
const fs=require('fs'),path=require('path'),assert=require('node:assert/strict');
const {chromium}=require('playwright');const root=path.resolve(__dirname,'..');
const read=p=>fs.readFileSync(path.join(root,p));
const demo=JSON.parse(read('design/demo-state.json'));demo.service_presets=JSON.parse(read('internal/pilot/service-presets.json'));
let update={supported:true,current:'0.2.0-alpha.10',engine:'v1.0.7-z2k-r0',stage:'idle',running:false,available:false};let installs=0,deviceReads=0,syncs=0,creates=0,deviceNull=true,checkRequests=0,checkGate=null;
let sourceUpdate={current:'0.2.0-alpha.10',supported:true,running:false,stage:'idle',sources:[{source:'ChatGPT',verification:'github-https-sha256',available:false,checked:false},{source:'Codex',verification:'github-https-sha256',available:false,checked:false}]};let sourceInstalls=0;
demo.checks={};
const dnsRequests=[];
const vlessActions=[];
const vlessState={available:true,configured:true,running:false,verified:false,milliseconds:123,socks:"127.0.0.1:10891"};
let subscriptionRequests=0;
(async()=>{const browser=await chromium.launch({headless:true,executablePath:process.env.PILOT_CHROMIUM});try{
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.route('http://pilot.test/**',async route=>{
  const req=route.request(),url=new URL(req.url()),json=(b,status=200)=>route.fulfill({json:b,status});
  if(url.pathname==='/auth/status')return json({configured:true,authenticated:true});
  if(url.pathname==='/api/status')return json(demo);
  if(url.pathname==='/api/dns/probe'){
   const request=req.postDataJSON();
   dnsRequests.push(request);
   return json(request.protocol==='router'
    ? [{protocol:'DNS роутера',server:'192.168.44.1',status:'ok',milliseconds:12},
       {protocol:'Прокси KeeneticOS',server:'127.0.0.1:40500',status:'configured',error:'Резолвер из текущего состояния DNS-прокси',milliseconds:0}]
    : request.protocol==='system' ? [{protocol:'System',server:'Системный резолвер ОС',status:'ok',milliseconds:2}]
    : [{protocol:'DoT',server:request.server,status:'unavailable',error:'kdig не найден в PATH'}]);
  }
  if(url.pathname==='/api/vpn/vless-routing')return json({
   selection:{devices:['192.168.1.44'],services:['tmdb']},
   devices:[{ip:'192.168.1.44',interface:'br0'}],services:[{id:'tmdb',name:'TMDB / Seerr'}]
  });
  if(url.pathname==='/api/vpn/vless'){
   if(req.method()==="POST") {
    const {action}=req.postDataJSON();
    vlessActions.push(action);
    vlessState.running=action!=="stop";
    vlessState.verified=action==="check";
   }
   return json(vlessState);
  }
  if(url.pathname==='/api/vpn/subscription') { subscriptionRequests++; return json({saved:false}); }
  if(url.pathname.startsWith('/api/vpn/subscription/')) { subscriptionRequests++; return json({error:'unexpected upload'},400); }
  if(url.pathname==='/api/device'){deviceReads++;return json({model:'KN-1811 · пример',platform:'linux/arm64',kernel:'test',cpu_cores:4,cpu_sample_available:true,cpu_percent:deviceNull?null:12.5,load:[0.2,0.1,0.1],memory_total:268435456,memory_available:134217728,uptime_seconds:90000,temperatures:[],wan:'ppp0',rx_bytes:1000,tx_bytes:500,rx_bytes_per_second:null,tx_bytes_per_second:null});}
  if(url.pathname==='/api/adaptive')return json(JSON.parse(read('design/demo-adaptive.json')));
  if(url.pathname==='/api/check'){checkRequests++;const id=req.postDataJSON().profile,p=demo.config.profiles.find(p=>p.id===id);if(checkGate)await new Promise(resolve=>{checkGate.resolve=resolve;});const results=[{url:p.targets[0].url,tls:'1.2',family:'tcp4',ok:true,status:200,duration_ms:145,attempt:1},{url:p.targets[0].url,tls:'1.3',family:'tcp4',ok:false,status:451,duration_ms:160,error:'HTTP 451 <img src=x onerror=alert(1)>',attempt:1}];demo.checks[id]={profile:id,started:new Date().toISOString(),finished:new Date().toISOString(),strategy:p.strategy,adaptive:false,running:true,complete:true,passed:false,stale:false,results};return json(results);}
  if(url.pathname==='/api/preflight')return json({checked:true,opt_free_bytes:100000000,storage_kind:'possibly_internal_nand',other_engines:['d2k'],installed_services:['S99d2k'],process_list_available:true,notes:['read-only only']});
  if(url.pathname==='/api/updates/sources')return json(sourceUpdate);
  if(url.pathname==='/api/updates/sources/check'){
    const arg=req.postDataJSON();assert.deepEqual(Object.keys(arg),['source']);
    if(arg.source==='ChatGPT')sourceUpdate.sources[0]={...sourceUpdate.sources[0],latest:'0.2.0-alpha.12',available:true,checked:true,commit:'b'.repeat(40)};
    else throw new Error('Unexpected Codex metadata request');
    return json(sourceUpdate);
  }
  if(url.pathname==='/api/updates/sources/install'){
    assert.deepEqual(req.postDataJSON(),{source:'ChatGPT',version:'0.2.0-alpha.12'});
    sourceInstalls++;sourceUpdate={...sourceUpdate,running:true,stage:'downloading'};update={...update,running:true,stage:'downloading',target:'0.2.0-alpha.12'};return json(sourceUpdate,202);
  }
  if(url.pathname==='/api/updates')return json(update);
  if(url.pathname==='/api/updates/check'){update={...update,latest:'0.2.0-alpha.11',latest_engine:'v-next',available:true};return json(update);}
  if(url.pathname==='/api/updates/install'){assert.deepEqual(req.postDataJSON(),{version:'0.2.0-alpha.11'});installs++;update={...update,running:true,stage:'downloading',target:'0.2.0-alpha.11'};return json(update,202);}
  if(url.pathname==='/api/services/custom'){creates++;const p=req.postDataJSON();demo.config.profiles.push(p);return json(demo);}
  if(url.pathname.endsWith('/domains')){syncs++;const id=url.pathname.split('/')[3],p=demo.config.profiles.find(p=>p.id===id);if(!p.domains.includes('static.customcdn.test'))p.domains.push('static.customcdn.test');p.domains_updated=new Date().toISOString();return json(demo);}
  if(url.pathname==='/api/config'){demo.config=req.postDataJSON();return json(demo);}
  if(url.pathname==='/api/services'){const s=demo.service_presets.find(s=>s.profile.id===req.postDataJSON().id);demo.config.profiles.push(structuredClone(s.profile));return json(demo);}
  const file=url.pathname==='/'?'index.html':url.pathname.slice(1);if(!['index.html','app.js','music.js','style.css','setup.css','favicon.svg'].includes(file))return route.abort();
  return route.fulfill({body:read('internal/pilot/web/'+file),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html'});
 });
 await page.goto('http://pilot.test/#device');await page.locator('#device-data h3').first().waitFor();

 // Music belongs to the browser, not the router or a service configuration.
 const musicDetails=page.locator('#pilot-music');
 await musicDetails.locator('summary').click();
 await page.locator('#radio-station').waitFor();
 await page.locator('#music-audio').waitFor({state:'attached'});
 assert.equal(await page.locator('#radio-station option').count(),24);
 await page.locator('#radio-search').fill('максимум');assert.equal(await page.locator('#radio-station option').count(),1);await page.locator('#radio-search').clear();
 assert.equal(await page.locator('#music-audio').evaluate(a=>a.autoplay),false);
 assert.equal(await page.locator('#music-audio').evaluate(a=>a.getAttribute('preload')),'none');
 assert.equal(await page.locator('#music-audio').evaluate(a=>a.hasAttribute('src')),false);
 await page.locator('#music-url').fill('http://insecure.test/music.mp3');
 await page.locator('#music-load-url').click();
 assert.match(await page.locator('#music-message').innerText(),/HTTPS/);
 assert.equal(await page.locator('#music-audio').evaluate(a=>a.hasAttribute('src')),false);
 await page.evaluate(()=>{document.querySelector('#music-audio').play=()=>Promise.resolve();});
 await page.locator('#music-url').fill('https://media.example.com/song.mp3');
 await page.locator('#music-load-url').click();
 assert.equal(await page.locator('#music-audio').evaluate(a=>a.getAttribute('src')),'https://media.example.com/song.mp3');
 await page.locator('#music-reset').click();
 assert.equal(await page.locator('#music-audio').evaluate(a=>a.hasAttribute('src')),false);
 await musicDetails.locator('summary').click();
 assert.equal(await musicDetails.evaluate(el=>el.open),false);
 await musicDetails.locator("summary").click();await page.locator("#content h1").click();assert.equal(await musicDetails.evaluate(el=>el.open),false);
 await musicDetails.locator("summary").click();await page.keyboard.press("Escape");assert.equal(await musicDetails.evaluate(el=>el.open),false);

 assert.match(await page.locator('#device-data').innerText(),/ожидается второй замер/);
 await page.locator('#preflight-data').getByText(/Другие запущенные обработчики/).waitFor();assert.match(await page.locator('#preflight-data').innerText(),/d2k/);
 assert.match(await page.locator('#device-data').innerText(),/Недоступно: прошивка/);
 deviceNull=false;await page.evaluate(()=>loadDevice());assert.match(await page.locator('#device-data').innerText(),/12.5 %/);
 assert.doesNotMatch(await page.locator('#device-data').innerText(),/0.0 °C/);
 await page.evaluate(()=>{location.hash='updates';});await page.locator('#update-check').waitFor();await page.waitForFunction(()=>updatesData!==null);
 update={...update,current:"0.2.0-beta.4"};await page.evaluate(()=>loadUpdates(true));const notes=page.locator("#installed-release-notes details");assert.match(await notes.innerText(),/DNS-тестер/);await notes.locator("summary").click();await page.evaluate(()=>loadUpdates(true));assert.equal(await notes.evaluate(el=>el.open),false);await notes.locator("summary").click();await page.evaluate(()=>loadUpdates(true));assert.equal(await notes.evaluate(el=>el.open),true);update={...update,current:"0.2.0-alpha.10"};
 demo.busy=true;await page.evaluate(()=>refresh());await page.evaluate(()=>loadUpdates());assert.equal(await page.locator('#update-check').isEnabled(),true,'Background search must not block version checks');
 await page.locator('#update-check').click();await page.waitForFunction(()=>updatesData.available);assert.equal(installs,0);
 assert.equal(await page.locator('#update-install').isEnabled(),true,'Verified update must be installable during background search');
 update={...update,checking:true};await page.evaluate(()=>loadUpdates());assert.equal(await page.locator('#update-check').isDisabled(),true,'Duplicate metadata check is blocked');update={...update,checking:false};
 await page.locator('#update-install').click();await page.waitForFunction(()=>updatesData.running);assert.equal(installs,1);assert.equal(await page.locator('#update-install').isDisabled(),true);await page.locator('#update-monitor[open]').waitFor();await page.waitForFunction(()=>document.querySelector('#update-monitor-state').textContent.includes('Загрузка'));await page.mouse.click(3,3);await page.locator("#update-monitor[open]").waitFor({state:"hidden"});assert.equal(installs,1);assert.equal(await page.locator("#update-install").isDisabled(),true);
 update={...update,stage:'waiting'};await page.evaluate(()=>loadUpdates());assert.match(await page.locator('#update-info').innerText(),/Ожидание завершения текущего подбора/);assert.equal(await page.locator('#update-check').isDisabled(),true);demo.busy=false;
 update={...update,current:'0.2.0-alpha.11',running:false,stage:'completed',available:false};await page.evaluate(()=>loadUpdates());assert.match(await page.locator('#update-info').innerText(),/Обновление завершено/);
 update={...update,stage:'failed',phase:'install',exit_code:23};await page.evaluate(()=>loadUpdates());assert.match(await page.locator('#update-info').innerText(),/установка приложения и движка/);assert.match(await page.locator('#update-info').innerText(),/код завершения: 23/);assert.match(await page.locator('#update-info').innerText(),/\/opt\/etc\/keenetic-pilot\/update.log/);
 await page.locator('[data-source-update-check="ChatGPT"]').waitFor();
 assert.match(await page.locator('#source-update-info').innerText(),/ChatGPT/);
 await page.locator('[data-source-update-check="ChatGPT"]').click();
 await page.waitForFunction(()=>sourceUpdatesData?.sources?.[0]?.available===true);
 assert.match(await page.locator('#source-update-info').innerText(),/0.2.0-alpha.12/);
 assert.match(await page.locator('#content').innerText(),/SHA-256/);
 page.on('dialog',dialog=>dialog.accept());
 await page.locator('[data-source-update-install="ChatGPT"]').click();
 await page.waitForFunction(()=>sourceUpdatesData?.running===true);
 assert.equal(sourceInstalls,1);await page.locator('#update-monitor[open]').waitFor();await page.waitForFunction(()=>document.querySelector('#update-monitor-state').textContent.includes('Загрузка'));await page.locator('#update-monitor-close').click();
 sourceUpdate.running=false;update={...update,running:false,stage:'completed',current:'0.2.0-alpha.12',exit_code:0,phase:''};

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
 assert.equal(await page.locator('#background_other_domains').isChecked(),false,'Unknown domains must not be probed automatically by default');
 assert.equal(await page.locator('#background_paused').count(),1);
 const profilesBeforeSettings=structuredClone(demo.config.profiles);
 await page.locator('#background_paused').check();
 await page.locator('#settings-form button[type=submit]').click();
 await page.waitForFunction(()=>state.config.background_paused===true);
 assert.equal(demo.config.background_other_domains,false);
 await page.locator('#background_other_domains').check();
 await page.locator('#settings-form button[type=submit]').click();
 await page.waitForFunction(()=>state.config.background_other_domains===true);
 assert.equal(demo.config.background_other_domains,true,'Explicit opt-in was not saved');
 assert.equal(demo.config.background_paused,true);
 await page.locator('#background_paused').uncheck();
 await page.locator('#background_other_domains').uncheck();
 await page.locator('#settings-form button[type=submit]').click();
 await page.waitForFunction(()=>state.config.background_other_domains===false);
 assert.equal(demo.config.background_other_domains,false,'Unknown-site search opt-out was not saved');
 assert.equal(demo.config.background_paused,false);
 assert.deepEqual(demo.config.profiles,profilesBeforeSettings);

 assert.equal(await page.locator('#adaptive_fast_start').isChecked(),true);
 const limits=[demo.config.adaptive_cooldown_seconds,demo.config.adaptive_max_switches,demo.config.adaptive_global_max_switches];
 await page.locator('#adaptive_fast_start').uncheck();await page.locator('#settings-form button[type=submit]').click();await page.waitForFunction(()=>state.config.adaptive_fast_start===false);
 assert.deepEqual([demo.config.adaptive_cooldown_seconds,demo.config.adaptive_max_switches,demo.config.adaptive_global_max_switches],limits,'Fast-start opt-out preserves configured limits');
 await page.evaluate(()=>{location.hash='geo';});
 const manualDNS=page.locator('#dns-manual-probe');
 await page.getByRole('button',{name:'Проверить DNS роутера'}).click();
 await page.locator('#dns-probe-result').getByText(/192.168.44.1.*Есть DNS-ответ/).waitFor();
 assert.equal(await manualDNS.isVisible(),false);
 await page.locator('summary').filter({hasText:'Ручная проверка внешнего DoH, DoT или DoQ'}).click();
 await page.locator('#dns-server').fill('dns.example');
 await page.locator('#dns-protocol').selectOption('DoT');
 await manualDNS.click();
 await page.locator('#dns-manual-result').getByText(/kdig не найден/).waitFor();
 assert.deepEqual(dnsRequests,[{protocol:'router'},{protocol:'DoT',server:'dns.example',address:''}]);
 await page.locator('#dns-probe').click();
 await page.locator('#dns-probe-result').getByText(/Есть DNS-ответ/).waitFor();
 assert.deepEqual(dnsRequests.at(-1),{protocol:'router'});
 await page.locator('summary').filter({hasText:'Системный DNS процесса Pilot'}).click();
 await page.locator('#dns-system-probe').click();
 await page.locator('#dns-system-result').getByText(/Системный резолвер ОС.*Есть DNS-ответ/).waitFor();
 assert.deepEqual(dnsRequests.at(-1),{protocol:'system'});
 assert.match(await page.locator('#dns-probe-result').innerText(),/192.168.44.1/);

 await page.locator('#vless-route-devices label').waitFor();
 assert.equal(await page.locator('#vless-route-devices input').isChecked(),true);
 assert.equal(await page.locator('#vless-route-services input').isChecked(),true);
 assert.equal(await page.locator('#vless-route-link').isDisabled(),true);
 assert.equal(await page.locator('#vless-routing-form button').isDisabled(),true);
 await page.getByRole('button',{name:'Подключить VLESS',exact:true}).click();
 await page.locator('#vless-connection-status').getByText(/Xray запущен/).waitFor();
 await page.getByRole('button',{name:'Проверить выход',exact:true}).click();
 await page.locator('#vless-connection-status').getByText(/HTTPS через VLESS подтверждён/).waitFor();
 await page.locator('#vless-stop').click();
 await page.locator('#vless-connection-status').getByText(/Профиль готов к запуску/).waitFor();
 assert.deepEqual(vlessActions,['start','check','stop']);
 const initialSubscriptionRequests=subscriptionRequests;
 const paste=page.locator('#vpn-subscription-text');
 await paste.fill('vless://example-id@test.example:443?security=reality#demo');
 await page.getByRole('button',{name:'Проверить VLESS'}).click();
 await page.getByRole('dialog').getByText(/vless: 1/).waitFor();
 assert.equal(subscriptionRequests,initialSubscriptionRequests);
 assert.ok(!(await page.getByRole('dialog').innerText()).includes('example-id'));
 await page.getByRole('button',{name:'Свернуть',exact:true}).click();
 await paste.fill('vless://first@test.example:443\nvless://second@test.example:443');
 await page.getByRole('button',{name:'Проверить VLESS'}).click();
 await page.getByRole('dialog').getByText(/vless: 2/).waitFor();
 assert.equal(subscriptionRequests,initialSubscriptionRequests);
 assert.deepEqual(errors,[]);console.log('PASS: router telemetry, missing temperature, signed-update UI flow, custom services, domain sources, Telegram Web, compact trials/methods, preserved expansion, mobile menus, Matrix overview, read-only service reports, safe error rendering, animated rain, reduced motion, hidden-tab pause, fast-start opt-out');
}finally{await browser.close();}})().catch(e=>{console.error(e);process.exitCode=1});
