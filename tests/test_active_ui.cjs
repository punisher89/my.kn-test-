// Synthetic active-search states. No router or public endpoint is contacted.
const assert=require('node:assert/strict'),fs=require('fs'),path=require('path');
const {chromium}=require('playwright');const root=path.resolve(__dirname,'..');
const read=p=>fs.readFileSync(path.join(root,p));
const demo=JSON.parse(read('design/demo-state.json')),adaptive=JSON.parse(read('design/demo-adaptive.json'));
const scope={host:'www.youtube.com',tls:'tls13',family:'ipv4',port:443};
demo.config.adaptive=true;demo.config.adaptive_active=true;demo.busy=true;demo.job=null;demo.domain_queue=2;demo.verified_domains=0;
demo.domain_job={scope,stage:'search',started:new Date().toISOString(),candidate:'fake-md5',candidate_index:4,candidate_total:27,trials:[{strategy:'direct:direct',passed:false,results:[{url:'https://www.youtube.com/',tls:'1.3',family:'tcp4',duration_ms:8000,error:'timeout'}]}]};
for(const r of [...adaptive.hosts,...adaptive.history,...adaptive.boxes.flatMap(b=>[...b.hosts,...b.history])])r.updated=Math.floor(Date.now()/1000)-5;
let cancels=0,retries=0;
(async()=>{const browser=await chromium.launch({headless:true,executablePath:process.env.PILOT_CHROMIUM});try{
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.route('http://pilot.test/**',async route=>{
  const req=route.request(),url=new URL(req.url()),json=(data,status=200)=>route.fulfill({json:data,status});
  if(url.pathname==='/auth/status')return json({configured:true,authenticated:true});
  if(url.pathname==='/api/status')return json(demo);
  if(url.pathname==='/api/tune/cancel'){cancels++;demo.busy=false;demo.domain_job.stage='cancelled';demo.domain_job.finished=new Date().toISOString();return json({status:'cancelling'},202);}
  if(url.pathname==='/api/domain-search'){assert.deepEqual(req.postDataJSON(),scope);retries++;demo.busy=true;demo.domain_job.stage='direct';delete demo.domain_job.finished;return json(demo,202);}
  if(url.pathname==='/api/adaptive')return json(adaptive);
  if(url.pathname==='/api/device')return json({cpu_cores:4,cpu_percent:null,cpu_sample_available:true,load:[],memory_total:0,memory_available:null,temperatures:[],wan:'ppp0'});
  const file=url.pathname==='/'?'index.html':url.pathname.slice(1);if(!['index.html','app.js','style.css','setup.css','favicon.svg'].includes(file))return route.abort();
  return route.fulfill({body:read('internal/pilot/web/'+file),contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html'});
 });
 await page.goto('http://pilot.test/#tune');await page.locator('.domain-search-report').waitFor();
 assert.match(await page.locator('.domain-search-report').innerText(),/Испытание методов/);
 assert.match(await page.locator('.domain-search-report').innerText(),/кандидат 4\/27/);
 assert.equal(await page.locator('#tune-cancel').isEnabled(),true,'Domain worker must be cancellable without a profile job');
 await page.locator('.domain-search-report [data-trial] > summary').click();await page.evaluate(()=>refresh());
 assert.equal(await page.locator('.domain-search-report [data-trial]').first().evaluate(el=>el.open),true,'Active report expansion survives polling');
 await page.locator('#tune-cancel').click();await page.locator('#domain-retry').waitFor();assert.equal(cancels,1);
 await page.locator('#domain-retry').click();await page.waitForFunction(()=>state.domain_job.stage==='direct');assert.equal(retries,1);
 demo.domain_job.stage='unmeasured';demo.domain_job.error='HTTP 403 <img src=x onerror=alert(1)>';demo.domain_job.finished=new Date().toISOString();demo.busy=false;
 await page.evaluate(()=>refresh());assert.match(await page.locator('.domain-search-report').innerText(),/Недостаточно данных/);assert.equal(await page.locator('.domain-search-report img').count(),0);
 demo.domain_job.reason='client_hello_missing';demo.domain_job.next_at=new Date(Date.now()+60000).toISOString();
 await page.evaluate(()=>refresh());assert.match(await page.locator('.domain-search-help').innerText(),/Открой этот сервис на проблемном устройстве/);assert.match(await page.locator('.domain-search-report').innerText(),/Следующая попытка/);
 demo.domain_job.reason='execution_unconfirmed';await page.evaluate(()=>refresh());assert.match(await page.locator('.domain-search-help').innerText(),/повторит этот кандидат автоматически/);
 demo.domain_job.stage='waiting';demo.domain_job.reason='resources';await page.evaluate(()=>refresh());assert.match(await page.locator('.domain-search-report').innerText(),/Поиск на паузе/);assert.match(await page.locator('.domain-search-help').innerText(),/снижения нагрузки/);
 demo.domain_job.reason='http_response';await page.evaluate(()=>refresh());assert.match(await page.locator('.domain-search-help').innerText(),/не подтверждает работу сервиса/);
 delete demo.domain_job.reason;delete demo.domain_job.next_at;
 demo.domain_job.stage='applied';demo.domain_job.selected='fake-md5';demo.domain_job.error='';demo.verified_domains=1;demo.domain_queue=0;
 await page.evaluate(()=>{location.hash='overview';});await page.locator('.domain-search-mini').waitFor();await page.evaluate(()=>refresh());assert.match(await page.locator('.domain-search-mini').innerText(),/Метод применён/);assert.match(await page.locator('.domain-search-mini').innerText(),/подтверждений: 1/);
 for(const b of adaptive.boxes)for(const r of b.hosts){r.applied_strategy='fake-md5';r.https_measured=true;}
 await page.evaluate(()=>{location.hash='adaptive';});await page.locator('[data-box]').first().waitFor();assert.match(await page.locator('[data-box]').first().innerText(),/HTTPS подтверждён/);
 await page.setViewportSize({width:390,height:844});for(const hash of ['overview','tune','adaptive','settings']){await page.evaluate(h=>{location.hash=h;},hash);await page.waitForFunction(h=>page===h,hash);assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,hash+' fits mobile');}
 assert.deepEqual(errors,[]);console.log('PASS: active domain progress, method index, cancellation, explicit retry, missing measurement, safe errors, applied-vs-observed methods, mobile layout');
}finally{await browser.close();}})().catch(e=>{console.error(e);process.exitCode=1});
