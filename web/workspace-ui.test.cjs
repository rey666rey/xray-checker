/* Run after WORKSPACE_CAPTURE_DIR=... go test ./web. All HTTP is intercepted. */
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');
const root = path.resolve(__dirname, '..');
const capture = process.env.WORKSPACE_CAPTURE_DIR;
if (!capture) throw new Error('WORKSPACE_CAPTURE_DIR must contain Go-rendered index.html and public.html');
(async () => {
 const browser = await chromium.launch({headless:true, ...(process.env.CHROMIUM_BIN ? {executablePath:process.env.CHROMIUM_BIN} : {})});
 const page = await browser.newPage({viewport:{width:1280,height:1000}});
 const errors = []; const writes=[]; const unexpected=[]; let disconnected=false; let networkReady=true; let finish=false;
 page.on('pageerror', e => errors.push(e.message));
 const now = Math.floor(Date.now()/1000);
 const proxies = ['🇩🇪 Германия (T1)','🇯🇵 Япония','🇦🇱 Албания','🇹🇷 Турция','🇩🇪 Германия (S24t)','🇫🇮 Финляндия'].map((name,i) => ({name,subName:['MASTER','LETO','220'][Math.floor(i/2)],protocol:'vless',stableId:'fixture-'+String.fromCharCode(97+i),nodeId:'node-'+String.fromCharCode(97+i),index:i,online:[1,3,5].includes(i),unstable:i===3,server:'192.0.2.'+(i+1),port:443,monitorState:i===0?'needs_replacement':i===3?'unstable':i===4?'unknown':'healthy',consecutiveFailures:i===0||i===2?3:0,lastCheck:i===4?0:now,latencyMs:[0,218,0,122,0,83][i]}));
 const state={version:1,enabled:true,activeSourceId:'iphone',epoch:'a'.repeat(32),updatedAt:now,profiles:[{id:'iphone',interface:'en7',available:true,address:'172.20.10.2'},{id:'router',interface:'',available:false}],interfaces:[{name:'en7',label:'iPhone USB',address:'172.20.10.2'},{name:'en9',label:'Ethernet',address:'192.168.8.2'}],job:{state:'idle'}};
 let telegram={configured:true,enabled:true,connected:true,managedByEnvironment:true,state:'connected',botUsername:'fixture_bot',recipient:'Test team',deliveryMode:'auto',availableRoutes:3,preferences:{criticalFailures:true,ipChanges:true,recoveries:true,newIpFailures:true,unstable:false,networkStatus:true,batchWindowSec:30,repeatHours:12}};
 await page.route('**/*', async route => {
  const req=route.request(); const u=new URL(req.url());
  if (u.hostname!=='xray.test') {unexpected.push(req.url());return route.abort();}
  if(u.pathname==='/'||u.pathname==='/public'){return route.fulfill({contentType:'text/html',body:fs.readFileSync(path.join(capture,u.pathname==='/public'?'public.html':'index.html'),'utf8')});}
  if(u.pathname.startsWith('/static/')) {const f=path.join(root,'web',u.pathname);return fs.existsSync(f)?route.fulfill({path:f}):route.fulfill({status:404,body:''});}
  if(disconnected) return route.abort();
  const write=!['GET','HEAD'].includes(req.method()); if(write) writes.push({path:u.pathname,method:req.method(),body:req.postData()?JSON.parse(req.postData()):null});
  let data={};
  if(u.pathname==='/api/v1/network/sources') {
   if(write) {const b=req.postDataJSON();state.job={id:'job-'+writes.length,action:b.action,sourceId:b.sourceId,state:'queued',message:'Waiting for host supervisor'};return route.fulfill({status:202,json:{success:true,data:state}});}
   if(finish&&state.job.state==='queued') {state.job.state='succeeded';state.job.message='Completed';if(state.job.action==='activate'){state.activeSourceId=state.job.sourceId;state.epoch='b'.repeat(32);state.profiles[1].interface='en9';state.profiles[1].available=true;state.profiles[1].address='192.168.8.2';}finish=false;}data=state;
  } else if(u.pathname==='/api/v1/network') data={managed:true,ready:networkReady,state:networkReady?'connected':'waiting',interface:'col0',sourceId:state.activeSourceId,label:state.activeSourceId==='iphone'?'iPhone USB':'LTE router',epoch:state.epoch,updatedAt:now,publicIp:'203.0.113.42',message:networkReady?'Control requests pass':'External control unavailable'};
  else if(u.pathname==='/api/v1/network/reconnect') data={enabled:true,active:false,state:'idle'};
  else if(u.pathname==='/api/v1/proxies'||u.pathname==='/api/v1/public/proxies')data=proxies;
  else if(u.pathname==='/api/v1/monitoring') data={mode:'failure_wave',pendingChecks:14,oldestDelaySeconds:5,queuedDiagnoses:2,recentFailures:3,recentRecoveries:1};
  else if(u.pathname==='/api/v1/nodes'||u.pathname==='/api/v1/access-checks')data=[];
  else if(u.pathname==='/api/v1/alerts/telegram') {if(write) {const b=req.postDataJSON();telegram={...telegram,enabled:b.enabled,deliveryMode:b.deliveryMode,preferences:b.preferences};}data=telegram;}
  else if(u.pathname==='/api/v1/alerts/telegram/test') data=telegram;
  else if(u.pathname.includes('/replacements/'))data={state:'current_verified'};
  else if(u.pathname.endsWith('/diagnose'))data={nodeId:'node-a',runId:'run1',state:'queued',stage:'queued',startedAt:now};
  else if(u.pathname.endsWith('/diagnosis'))data=[];
  else {unexpected.push(u.pathname);return route.fulfill({status:404,json:{success:false}});}
  return route.fulfill({json:{success:true,data}});
 });
 const data=fn=>page.evaluate(fn);
 const refresh=()=>data(async()=>{const d=Alpine.$data(document.documentElement);await d.refreshData();await d.refreshNetworkStatus();});
 const nav=name=>page.locator('.ws-nav').getByRole('button',{name,exact:true}).click();
 await page.goto('http://xray.test/');await page.locator('.ws-panel-card').first().waitFor();await refresh();
 assert.equal(await page.locator('.ws-panel-card').count(),3);assert.equal(await data(()=>Alpine.$data(document.documentElement).stats.total),2);
 await page.locator('[data-panel="LETO"]').click();assert.equal(await data(()=>Alpine.$data(document.documentElement).selectedPanel),'LETO');assert.equal(await page.locator('.ws-host-card').count(),2);
 await page.getByRole('button',{name:'Требуют внимания',exact:true}).click();assert.equal(await page.locator('.ws-host-card').count(),2);
 await page.getByRole('button',{name:'Все',exact:true}).click();await page.getByRole('searchbox',{name:'Поиск хоста или IP'}).fill('Албания');await page.waitForTimeout(250);assert.equal(await page.locator('.ws-host-card').count(),1);
 await page.getByRole('searchbox').fill('');await page.waitForTimeout(250);
 await page.getByRole('button',{name:'Все панели',exact:true}).click();assert.equal(await page.locator('.ws-group-heading').count(),3);assert.equal(await page.locator('.ws-host-card').count(),6);
 await page.getByRole('button',{name:'Подробнее: 🇯🇵 Япония',exact:true}).click();assert.equal(await page.locator('.ws-inspector h3').innerText(),'🇯🇵 Япония');
 await page.getByRole('button',{name:'Перепроверить: 🇯🇵 Япония',exact:true}).click();assert(writes.some(w=>w.path==='/api/v1/replacements/fixture-b/verify'));
 await page.locator('[data-panel="MASTER"]').click();await page.getByRole('button',{name:'Диагностика узла',exact:true}).click();assert(writes.some(w=>w.path==='/api/v1/nodes/node-a/diagnose'));await data(()=>Alpine.$data(document.documentElement).closeDiagnosis());
 // A source outage must not turn historical green/red cards into current claims.
 networkReady=false;await refresh();assert.equal(await page.locator('.ws-host-card').first().getAttribute('data-tone'),'muted');assert.match(await page.locator('.ws-host-card').first().innerText(),/Сохранённый результат/);assert.equal(await page.getByRole('button',{name:'Перепроверить: 🇩🇪 Германия (T1)',exact:true}).isDisabled(),true);
 networkReady=true;await refresh();
 // Burst-sized fixtures must not recompute/sort the full list for each card selection.
 const burstMs=await data(async()=>{const d=Alpine.$data(document.documentElement), original=d.proxies, start=performance.now();d.choosePanel('');d.proxies=Array.from({length:450},(_,i)=>({...original[i%original.length],stableId:'burst-'+i,name:original[i%original.length].name+' / '+i}));await d.$nextTick();const ms=performance.now()-start;d.proxies=original;await d.$nextTick();return Math.round(ms);});
 // Source preflight and cancel are non-activating. Activation only follows confirmation.
 await nav('Сеть проверок');await page.locator('#source-interface-router').selectOption('en9');const router=page.locator('.ws-source-profile').filter({has:page.getByRole('heading',{name:'LTE-роутер',exact:true})});
 await router.getByRole('button',{name:'Проверить адаптер'}).click();await page.waitForFunction(()=>!Alpine.$data(document.documentElement).networkSourceSubmitting);assert.equal(writes.at(-1).body.action,'validate');assert.equal(state.activeSourceId,'iphone');finish=true;await data(()=>Alpine.$data(document.documentElement).refreshNetworkSources());
 let n=writes.length;await router.getByRole('button',{name:'Использовать эту сеть'}).click();await router.getByRole('button',{name:'Отмена',exact:true}).click();assert.equal(writes.length,n);
 await router.getByRole('button',{name:'Использовать эту сеть'}).click();await router.getByRole('button',{name:'Переключить',exact:true}).click();await page.waitForFunction(()=>!Alpine.$data(document.documentElement).networkSourceSubmitting);assert.equal(writes.at(-1).body.action,'activate');assert.equal(writes.at(-1).body.expectedEpoch,'a'.repeat(32));finish=true;await refresh();await refresh();assert.equal(await page.getByRole('button',{name:'Reconnect iPhone bridge'}).isVisible(),false);
 // Telegram is a navigable page; draft survives quiet polling, blocks navigation, saves real payload.
 await nav('Telegram-алерты');await page.locator('.ws-alert-layout').waitFor();assert.equal(await data(()=>Alpine.$data(document.documentElement).activeView),'alerts');assert.equal(await page.locator('.alerts-backdrop').count(),0);
 await page.getByRole('checkbox',{name:/Нестабильные узлы/}).check();await data(()=>Alpine.$data(document.documentElement).loadTelegramStatus(true));assert.equal(await page.getByRole('checkbox',{name:/Нестабильные узлы/}).isChecked(),true);
 await nav('Мониторинг');await page.getByRole('alertdialog').waitFor();assert.equal(await data(()=>Alpine.$data(document.documentElement).activeView),'alerts');await page.getByRole('button',{name:'Продолжить редактирование'}).click();
 await page.getByRole('button',{name:'Сохранить настройки',exact:true}).click();await page.waitForFunction(()=>!Alpine.$data(document.documentElement).telegramBusy);assert.equal(writes.at(-1).method,'PUT');assert.equal(writes.at(-1).body.preferences.unstable,true);assert.equal(writes.at(-1).body.token,'');assert.equal(writes.at(-1).body.chatId,0);
 n=writes.length;await page.locator('.ws-alert-preview select').selectOption('networkStatus');assert.equal(writes.length,n,'preview must not send');assert.match(await page.locator('.ws-message').innerText(),/не отправлен/);
 await page.getByRole('button',{name:'Отправить тест в Telegram',exact:true}).click();await page.waitForFunction(()=>!Alpine.$data(document.documentElement).telegramBusy);assert.equal(writes.at(-1).path,'/api/v1/alerts/telegram/test');
 await page.getByRole('checkbox',{name:/Нестабильные узлы/}).uncheck();await nav('Сеть проверок');await page.getByRole('button',{name:'Сбросить и перейти',exact:true}).click();assert.equal(await data(()=>Alpine.$data(document.documentElement).activeView),'network');
 await data(()=>{Alpine.$data(document.documentElement).toasts=[];});
 // Responsive checks for every real screen in both themes.
 const layouts=[];
 for(const light of [false,true]) {
  await data(()=>{Alpine.$data(document.documentElement).darkMode=false;});if(!light)await data(()=>{Alpine.$data(document.documentElement).darkMode=true;});
  for(const view of ['Мониторинг','Сеть проверок','Telegram-алерты']) {
   await nav(view);
   if(view==='Telegram-алерты') {await page.locator('.ws-alert-layout').waitFor();assert.equal(await page.locator('.ws-alert-preview select').inputValue(),await data(()=>Alpine.$data(document.documentElement).alertPreviewKind));}
   for(const width of [1440,1100,768,540,392,352]) {
    await page.setViewportSize({width,height:1000});await page.waitForTimeout(35);
    const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1);assert.equal(overflow,false,`${view} width=${width} light=${light}`);
    layouts.push(`${view}:${width}:${light?'light':'dark'}`);
    if(!light&&[1100,392].includes(width))await page.screenshot({path:path.join(capture,`${view==='Мониторинг'?'monitor':view==='Сеть проверок'?'network':'alerts'}-${width}.png`),fullPage:true});
   }
  }
 }
 // Draft guard must also cover token setup, including reload state.
 await nav('Мониторинг');disconnected=true;await refresh();assert.equal(await data(()=>Alpine.$data(document.documentElement).dashboardConnected),false);assert.equal(await data(()=>Alpine.$data(document.documentElement).networkSources.enabled),false);disconnected=false;await refresh();
 telegram={...telegram,configured:false};await nav('Telegram-алерты');await page.getByRole('button',{name:'Проверить и продолжить'}).waitFor();await data(()=>{Alpine.$data(document.documentElement).telegramToken='fixture-secret';});await nav('Мониторинг');await page.getByRole('alertdialog').waitFor();await page.getByRole('button',{name:'Сбросить и перейти'}).click();assert.equal(await data(()=>Alpine.$data(document.documentElement).telegramToken),'');
 await page.goto('http://xray.test/public');assert.equal(await page.locator('.workspace-app').count(),0);assert.equal(await page.getByRole('button',{name:'Использовать эту сеть'}).count(),0);
 assert.deepEqual(errors,[]);assert.deepEqual(unexpected,[]);await browser.close();console.log(JSON.stringify({result:'PASS',panels:'isolated',diagnosis:'live API',source:'explicit confirmation',telegram:'page + durable preferences + draft guard',public:'no controls',layouts:layouts.length,burstCards:450,burstRenderMs:burstMs,runtimeErrors:0}));
})().catch(e=>{console.error(e);process.exit(1);});
