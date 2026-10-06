const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const flush=()=>new Promise(resolve=>setImmediate(resolve));
function fixture({latest=null,request}={}){
 let html='',alive=true;const buttons=new Map(),requests=[],timers=new Map(),moves=[];let seq=0;
 const host={get innerHTML(){return html;},set innerHTML(v){html=v;buttons.clear();for(const key of ['data-import-all','data-import-retry','data-import-refresh','data-import-jobs'])if(v.includes(key))buttons.set('['+key+']',{});if(v.includes('<details'))buttons.set('details',{open:v.includes('source-bulk-details" open')});},querySelector:key=>buttons.get(key)||null,querySelectorAll:()=>[]};
 const api=async(path,method,body)=>{requests.push({path,method,body});if(request)return request(path,method,body);return latest;};
 const ctx={setTimeout:(fn,ms)=>{const id=++seq;timers.set(id,{fn,ms});return id;},clearTimeout:id=>timers.delete(id)};vm.createContext(ctx);vm.runInContext(fs.readFileSync(__dirname+'/source_bulk.js','utf8'),ctx);
 const stop=ctx.CampusSourceBulk.mount(host,{api,esc:v=>String(v??'').replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('"','&quot;'),D:{date:()=> '今天'},active:()=>alive,navigate:(...args)=>moves.push(args),count:40});
 return {requests,timers,moves,stop,html:()=>html,button:key=>buttons.get('['+key+']'),leave:()=>{alive=false;},async poll(){const [id,t]=timers.entries().next().value;timers.delete(id);assert.equal(t.ms,5000);await t.fn();await flush();}};
}
function batch(state='RUNNING'){return {id:'batch-one',state,created_at:'now',completed:1,failed:state==='COMPLETED_WITH_ERRORS'?1:0,imported:12,items:[{company:'百度<script>',scope:'应届校招',state:'SUCCESS',expected:12,completed:12},{company:'美团',scope:'应届生',state:state==='COMPLETED_WITH_ERRORS'?'FAILED':'FETCHING',expected:30,completed:2,code:'SOURCE_IMPORT_BLOCKED'}]};}
test('reload restores persisted progress, polls background work, and escapes source text',async()=>{
 const h=fixture({latest:batch()});await flush();assert.match(h.html(),/来源 1\/2 · 已读取 12 个岗位/);assert.match(h.html(),/百度&lt;script>/);assert.doesNotMatch(h.html(),/百度<script>/);assert.match(h.html(),/2\/30 个岗位/);assert.equal(h.timers.size,1);assert.equal(h.requests.filter(r=>r.method==='POST').length,0);await h.poll();assert.equal(h.requests.length,2);h.stop();assert.equal(h.timers.size,0);
});
test('one click sends empty server-owned selection and suppresses duplicate submissions',async()=>{
 let release;const pending=new Promise(resolve=>release=resolve);
 const h=fixture({request:async(path,method)=>method==='POST'?pending:null});await flush();const click=h.button('data-import-all').onclick;const first=click();click();assert.equal(h.requests.filter(r=>r.method==='POST').length,1);assert.deepEqual(JSON.parse(JSON.stringify(h.requests.at(-1))),{path:'/api/sources/import-all',method:'POST',body:{}});release(batch());await first;assert.match(h.html(),/正在后台导入/);assert.equal(h.timers.size,1);h.stop();
});
test('retry targets only the latest failed batch and preserves source progress',async()=>{
 const h=fixture({request:async(path,method)=>method==='POST'?batch():batch('COMPLETED_WITH_ERRORS')});await flush();assert.match(h.html(),/官网限制了访问/);assert.equal(h.timers.size,0);await h.button('data-import-retry').onclick();assert.equal(h.requests.at(-1).path,'/api/sources/imports/batch-one/retry');assert.deepEqual(JSON.parse(JSON.stringify(h.requests.at(-1).body)),{});assert.match(h.html(),/来源 1\/2/);h.stop();
});
test('leaving the page prevents late writes and stops polling without cancelling the task',async()=>{
 let release;const pending=new Promise(resolve=>release=resolve);const h=fixture({request:()=>pending});const before=h.html();h.leave();release(batch());await flush();assert.equal(h.html(),before);assert.equal(h.timers.size,0);assert.equal(h.requests.length,1);h.stop();
});
test('a stale refresh cannot overwrite a newly submitted retry',async()=>{
 let release;let reads=0;const stale=new Promise(resolve=>release=resolve);
 const h=fixture({request:async(path,method)=>method==='POST'?batch():++reads===1?batch('COMPLETED_WITH_ERRORS'):stale});await flush();
 const refresh=h.button('data-import-refresh').onclick();await h.button('data-import-retry').onclick();release(batch('COMPLETED_WITH_ERRORS'));await refresh;await flush();
 assert.match(h.html(),/正在后台导入/);assert.equal(h.timers.size,1);h.stop();
});
test('cooldown is actionable and a failed request never claims completion',async()=>{
 const h=fixture({request:async(path,method)=>{if(method==='POST')throw Object.assign(new Error('通用提示'),{code:'SOURCE_IMPORT_COOLDOWN'});return null;}});await flush();await h.button('data-import-all').onclick();assert.match(h.html(),/每 30 分钟/);assert.doesNotMatch(h.html(),/本轮导入完成/);assert.equal(h.timers.size,0);h.stop();
});
test('navigation opens the job radar without submitting another import',async()=>{
 const h=fixture({latest:batch('COMPLETED')});await flush();h.button('data-import-jobs').onclick();assert.deepEqual(h.moves,[['matching']]);assert.equal(h.requests.length,1);assert.equal(h.timers.size,0);h.stop();
});
