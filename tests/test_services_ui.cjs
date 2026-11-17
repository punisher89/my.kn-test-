// Synthetic browser flow for service presets and compact profile editing.
const assert=require('node:assert/strict'), fs=require('node:fs'), path=require('node:path');
const {chromium}=require('playwright');
const root=path.resolve(__dirname,'..');
const read=(p)=>fs.readFileSync(path.join(root,p));
const demo=JSON.parse(read('design/demo-state.json'));
demo.service_presets=JSON.parse(read('internal/pilot/service-presets.json'));
let additions=0,tunes=0,failNext=false;
let relay={worker_host:'',status:'draft_only',can_route:false,source:'t0fox/d2o design reference',uses_external_worker:false,notes:[]};let relayWrites=0;
(async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.PILOT_CHROMIUM});
 try{
  const page=await browser.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.route('http://pilot.test/**',async route=>{
   const url=new URL(route.request().url()),method=route.request().method();
   const json=(body,status=200)=>route.fulfill({status,json:body});
   if(url.pathname==='/auth/status')return json({configured:true,authenticated:true});
   if(url.pathname==='/api/status')return json(demo);
   if(url.pathname==='/api/telegram/relay-draft'){
     if(method==='PUT'){
       assert.deepEqual(Object.keys(route.request().postDataJSON()),['worker_host']);
       const host=route.request().postDataJSON().worker_host;
       relay={...relay,worker_host:host,uses_external_worker:Boolean(host)};
       relayWrites++;return json(relay);
     }
     return json(relay);
   }
   if(url.pathname==='/api/services'&&method==='POST'){
    if(failNext){failNext=false;return json({error:'Тестовая ошибка добавления'},500);}
    const body=route.request().postDataJSON();assert.deepEqual(Object.keys(body),['id']);
    const preset=demo.service_presets.find(s=>s.profile.id===body.id);
    if(demo.config.profiles.some(p=>p.id===body.id))return json({error:'duplicate'},400);
    additions++;demo.config.profiles.push(structuredClone(preset.profile));return json(demo);
   }
   if(url.pathname.startsWith('/api/services/')&&method==='DELETE'){
    demo.config.profiles=demo.config.profiles.filter(p=>p.id!==url.pathname.split('/').at(-1));return json(demo);
   }
   if(url.pathname==='/api/config'&&method==='PUT'){demo.config=route.request().postDataJSON();return json(demo);}
   if(url.pathname==='/api/tune'&&method==='POST'){
    tunes++;demo.busy=true;demo.job={profile:route.request().postDataJSON().profile,stage:'direct',started:new Date().toISOString(),trials:[]};return json(demo,202);
   }
   const file=url.pathname==='/'?'index.html':url.pathname.slice(1);
   if(!['index.html','app.js','music.js','style.css','setup.css','favicon.svg'].includes(file))return route.abort();
   return route.fulfill({body:read('internal/pilot/web/'+file),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html'});
  });
  await page.goto('http://pilot.test/#profiles');
  const profiles=page.locator('[data-profile-card]');await profiles.first().waitFor();
  assert.equal(await profiles.count(),2);
  assert.equal(await profiles.first().evaluate(el=>el.open),false);
  const youtube=page.locator('[data-profile-card="youtube"]');
  await youtube.locator('summary').first().click();
  await page.locator('#strategy-youtube').selectOption('none');
  await youtube.locator('details > summary').first().click();
  const original=demo.config.profiles[0].strategy;
  await page.locator('#domains-youtube').fill(demo.config.profiles[0].domains.join('\n')+'\nextra.example.com');
  await page.locator('#service-catalog > summary').click();
  const search=page.locator('#service-search');await search.fill('ИНСТАГРАМ');
  assert.equal(await page.locator('[data-service]').count(),1);
  failNext=true;await page.locator('[data-add-service="instagram"]').click();
  await page.waitForFunction(()=>!serviceMutationPending);
  assert.match(await page.locator('#toast').innerText(),/Тестовая ошибка/);
  assert.equal(demo.config.profiles.length,2);
  await page.locator('[data-add-service="instagram"]').click();
  await page.locator('[data-profile-card="instagram"]').waitFor();
  assert.equal(additions,1);
  assert.equal(demo.config.profiles[0].strategy,original);
  assert.equal(await page.locator('#strategy-youtube').inputValue(),'none');
  assert.match(await page.locator('#domains-youtube').inputValue(),/extra.example.com/);
  assert.equal(await youtube.evaluate(el=>el.open),true);
  assert.equal(await page.locator('[data-add-service="instagram"]').isDisabled(),true);
  assert.equal(demo.config.profiles.at(-1).strategy,'none');
  await page.locator('[data-save-profile="youtube"]').click();
  await page.waitForFunction(()=>!serviceMutationPending);
  assert.equal(demo.config.profiles[0].strategy,'none');
  assert.ok(demo.config.profiles[0].domains.includes('extra.example.com'));
  if(!await page.locator('[data-expand-domains="youtube"]').isVisible())await youtube.locator('details > summary').first().click();
  await page.locator('[data-expand-domains="youtube"]').click();await page.waitForFunction(()=>!serviceMutationPending);
  assert.ok(demo.config.profiles[0].domains.includes('extra.example.com'),'Local domains survive catalog expansion');
  assert.ok(demo.config.profiles[0].domains.includes('youtube.googleapis.com'));assert.ok(demo.config.profiles[0].domains.includes('yt3.googleusercontent.com'));assert.ok(demo.config.profiles[0].domains.length>150);
  assert.equal(demo.config.profiles[0].strategy,'none','Expansion preserves selected strategy');
  assert.equal(await page.evaluate(()=>{const p={id:'x',domains:['own.example.com'],source_exclusions:['excluded.example.com']},preset={profile:{domains:['excluded.example.com','occupied.example.com','new.example.com']}};return expandedProfileDomains(p,preset,[p,{id:'other',domains:['occupied.example.com']}]).join(',');}),'own.example.com,new.example.com');
  await page.evaluate(()=>{location.hash='tune';});await page.locator('#tune-profile').waitFor();
  await page.locator('[data-preset-select="instagram"]').click();
  assert.equal(await page.locator('#tune-profile').inputValue(),'instagram');
  await search.fill('chatgpt');await page.locator('[data-add-service="chatgpt"]').click();
  await page.waitForFunction(()=>!serviceMutationPending);
  assert.equal(await page.locator('#tune-profile').inputValue(),'chatgpt');
  assert.match(await page.locator('#tune-profile-note').innerText(),/региона или аккаунта/);
  assert.equal(tunes,0,'Adding a service must not silently start tuning');
  await page.locator('#tune-start').click();await page.waitForFunction(()=>state.busy);
  assert.equal(tunes,1);
  await search.fill('signal.org');assert.equal(await page.locator('[data-add-service="signal"]').isDisabled(),true);
  demo.busy=false;demo.job.finished=new Date().toISOString();await page.evaluate(()=>refresh());
  assert.equal(await page.locator('[data-add-service="signal"]').isEnabled(),true,'Controls recover when a job finishes');
  await page.evaluate(()=>{location.hash='profiles';});await profiles.first().waitFor();
  await page.locator('[data-profile-card="instagram"] > summary').click();
  await page.locator('[data-remove-service="instagram"]').click();
  await page.locator('[data-profile-card="instagram"]').waitFor({state:'detached'});
  assert.equal(demo.config.profiles[0].strategy,'none');
  for(const id of ['instagram','facebook','twitter','whatsapp','signal']){
   await search.fill(id);await page.locator(`[data-add-service="${id}"]`).click();await page.waitForFunction(()=>!serviceMutationPending);
  }
  assert.equal(await profiles.count(),8);
  await profiles.evaluateAll(items=>items.forEach(el=>{el.open=false;}));
  assert.ok((await profiles.evaluateAll(items=>items.map(el=>el.getBoundingClientRect().height))).every(height=>height<120),'Profiles stay compact when collapsed');
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Profile page fits mobile width');
  await page.evaluate(()=>{location.hash='tune';});await page.locator('#tune-profile').waitFor();
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Tuner fits mobile width');
  // Restore YouTube from the same panel without altering unrelated profiles.
  await page.evaluate(()=>{location.hash='profiles';});
  await page.locator('#youtube-restore').waitFor();
  page.on('dialog',async dialog=>dialog.accept());
  const otherBefore=demo.config.profiles.filter(p=>p.id!=='youtube').map(p=>({id:p.id,strategy:p.strategy}));
  await page.locator('#youtube-restore').click();
  await page.waitForFunction(()=>state.config.youtube_stable===true);
  assert.equal(demo.config.profiles.find(p=>p.id==='youtube').strategy,'fake-host-md5');
  assert.ok(demo.config.profiles.find(p=>p.id==='youtube').domains.includes('googlevideo.com'),'Restore must cover YouTube video CDN');
  assert.deepEqual(demo.config.profiles.filter(p=>p.id!=='youtube').map(p=>({id:p.id,strategy:p.strategy})),otherBefore);
  // Native Telegram must be available without creating the Telegram Web profile.
  assert.ok(!demo.config.profiles.some(p=>p.id==="telegram-web"));
  const nativePanel=page.locator("#telegram-native-panel");
  await nativePanel.waitFor();
  assert.match(await nativePanel.innerText(),/приложение \(MTProto TCP\)/);
  const profilesBefore=JSON.stringify(demo.config.profiles);
  await page.locator("#telegram-native-toggle").click();
  await page.waitForFunction(()=>state.config.telegram_mtproto===true);
  assert.equal(demo.config.telegram_mtproto,true);
  assert.equal(JSON.stringify(demo.config.profiles),profilesBefore,"MTProto enable must not rewrite YouTube or Web profiles");
  assert.match(await nativePanel.innerText(),/MTProto TCP включён/);
  await page.locator("#telegram-native-toggle").click();
  await page.waitForFunction(()=>state.config.telegram_mtproto===false);
  assert.equal(JSON.stringify(demo.config.profiles),profilesBefore);
  const relayPanel=page.locator("#telegram-relay-panel");
  await relayPanel.waitFor();
  assert.match(await relayPanel.innerText(),/Не активен/);
  assert.equal(await page.locator("#telegram-relay-host").inputValue(),"");
  await page.locator("#telegram-relay-host").fill("my-telegram.example.workers.dev");
  await page.locator("#telegram-relay-draft-form button[type=submit]").click();
  await page.waitForFunction(()=>telegramRelayDraftData?.worker_host==="my-telegram.example.workers.dev");
  assert.equal(relayWrites,1,"expected only a draft save");
  assert.equal(JSON.stringify(demo.config.profiles),profilesBefore,"Relay template must not modify native routing");
  assert.match(await relayPanel.innerText(),/Прокси НЕ запущен|Прокси и перенаправление выключены/);

  // A one-click recovery reduces load but must preserve unrelated profiles and MTProto.
  const before=JSON.stringify(demo.config.profiles.filter(p=>p.id!=="youtube"));
  const nativeSetting=demo.config.telegram_mtproto;
  await page.locator("#performance-rescue-apply").click();
  await page.waitForFunction(()=>state.config.adaptive===false && state.config.youtube_stable===true);
  assert.equal(demo.config.adaptive_active,false);
  assert.equal(demo.config.auto,false);
  assert.equal(demo.config.quic,false);
  assert.equal(demo.config.profiles.find(p=>p.id==="youtube").strategy,"fake-host-md5");
  assert.equal(JSON.stringify(demo.config.profiles.filter(p=>p.id!=="youtube")),before);
  assert.equal(demo.config.telegram_mtproto,nativeSetting);
  assert.match(await page.locator("#performance-rescue").innerText(),/Режим включён/);
  assert.deepEqual(errors,[]);
  console.log('PASS: service presets, add/remove, error recovery, preserved drafts, compact profiles, tuner selection, explicit tuning, busy recovery, mobile layout');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
