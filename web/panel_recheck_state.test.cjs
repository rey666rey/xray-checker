// Exercise the rendered recheck model with in-memory HTTP responses only.
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const html = fs.readFileSync(process.argv[2], 'utf8');
const script = [...html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/g)].map(m => m[1]).find(s => s.includes('function dashboard()'));
let requests = [], respond;
const factory = vm.runInNewContext('(' + script.slice(script.indexOf('function dashboard()')).trim() + ')', {
 localStorage:{getItem:()=>null,setItem(){}}, getUrlParam:()=>false,
 URLSearchParams, window:{location:{search:''}}, console,
 fetch:(url,options)=>{requests.push({url,options});return new Promise((resolve,reject)=>{respond={resolve,reject};});},
});
function setup() {
 requests=[];
 const app=factory();app.networkStatus={managed:true,ready:true};app.$nextTick=()=>{};
 const proxy={...app.proxies[0],stableId:'test/a b',rechecking:false};app.proxies=[proxy];
 app.showToast=text=>{app.lastToast=text;};
 app.refreshData=async()=>{app.refreshCount=(app.refreshCount||0)+1;app.proxies=app.proxies.map(p=>({...p}));};
 return {app,proxy};
}
(async()=>{
 const {app,proxy}=setup();
 assert.equal(app.canRecheckProxy(null),false);assert.equal(app.canRecheckProxy({}),false);
 app.networkStatus.ready=false;await app.recheckProxy(proxy);assert.equal(requests.length,0);
 app.networkStatus.ready=true;
 const pending=app.recheckProxy(proxy);
 assert.equal(proxy.rechecking,true);assert.equal(app.canRecheckProxy(proxy),false);
 await app.recheckProxy(proxy);assert.equal(requests.length,1);
 assert.equal(requests[0].url,'./api/v1/replacements/test%2Fa%20b/verify');
 assert.equal(requests[0].options.method,'POST');
 respond.resolve({ok:true,json:async()=>({success:true,data:{state:'current_verified'}})});
 await pending;
 assert.equal(app.lastToast,'Перепроверка завершена · узел работает');
 assert.equal(app.refreshCount,1);assert.equal(proxy.rechecking,false);assert.equal(app.proxies[0].rechecking,false);
 assert.equal(app.canRecheckProxy(app.proxies[0]),true);
 for(const [state,text] of [
  ['verified','Замена проверена · 192.0.2.8'],['unstable','Соединение нестабильно · повторите проверку'],
  ['failed','Узел не прошёл перепроверку'],['ambiguous','Найдено несколько новых серверов · выберите новую карточку'],
  ['not_received','Новый сервер ещё не получен от панели'],
 ]) {
  const {app,proxy}=setup();const work=app.recheckProxy(proxy);
  respond.resolve({ok:true,json:async()=>({success:true,data:{state,currentAddress:'192.0.2.8'}})});await work;
  assert.equal(app.lastToast,text);assert.equal(app.proxies[0].rechecking,false);
 }
 for(const fail of ['http','network']) {
  const {app,proxy}=setup();const work=app.recheckProxy(proxy);
  if(fail==='http') respond.resolve({ok:false,json:async()=>({success:false,error:'fixture error'})});
  else respond.reject(new Error('fixture error'));
  await work;assert.equal(app.lastToast,'fixture error');assert.equal(proxy.rechecking,false);assert.equal(app.refreshCount,undefined);
 }
 console.log('RECHECK_STATE_PASS: POST/duplicate-guard/network-gate/busy/refresh/results/errors=PASS');
})().catch(err=>{console.error(err);process.exitCode=1;});
