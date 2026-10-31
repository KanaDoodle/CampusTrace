const {test}=require('node:test'),assert=require('node:assert/strict');
const {mount}=require('./recruiting_reminders.js'),{esc}=require('./ui.js');
function host(){
 let html='',nodes=[];const details={open:false};
 return {get innerHTML(){return html;},set innerHTML(v){html=v;nodes=[...v.matchAll(/<button[^>]*\b(data-reminder-[a-z]+)(?:="([^"]*)")?[^>]*>/g)].map(m=>({attribute:m[1],dataset:{[m[1].slice(5).replace(/-([a-z])/g,(_,c)=>c.toUpperCase())]:m[2]},disabled:false}));},querySelector(s){return s==='details'?details:nodes.find(n=>s==='['+n.attribute+']')||null;},querySelectorAll(s){return s==='button'?nodes:nodes.filter(n=>s==='['+n.attribute+']');}};
}
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const item=(id='a'.repeat(32))=>({id,job_id:'b'.repeat(32),company:'合成公司',title:'<img onerror=alert(1)>',explanation:'岗位资料已变化'});
test('job-change reminders open current linked application dossiers without starting work',()=>{
 const application='c'.repeat(32),h=host(),seen=[],state={value:{enabled:true,items:[{...item(),related_applications:[{id:application,current_state:'APPLIED'},{id:'bad-id'}]}]}};
 mount(h,{api:assert.fail,esc,navigate:(...args)=>seen.push(args),state});assert.ok(h.innerHTML.includes('核对申请留档'));assert.ok(!h.innerHTML.includes('bad-id'));
 h.querySelector('[data-reminder-application]').onclick();assert.deepEqual(seen,[['dossier',{applicationID:application}]]);
});
test('Today redraws share the read and discarded hosts cannot paint its reply',async()=>{
 let resolve,reads=0;const state={},a=host(),b=host(),api=()=>{reads++;return new Promise(r=>resolve=r);};
 const one=mount(a,{api,esc,navigate(){},state});one.dispose();mount(b,{api,esc,navigate(){},state});assert.equal(reads,1);
 resolve({enabled:true,items:[item()]});await tick();assert.ok(!a.innerHTML.includes('合成公司'));assert.ok(b.innerHTML.includes('合成公司'));assert.ok(b.innerHTML.includes('&lt;img'));assert.ok(!b.innerHTML.includes('<img'));
});
test('A failed refresh retains the last reminders and a retry can recover',async()=>{
 let fail=true;const state={value:{enabled:true,items:[item()]}},h=host();
 const view=mount(h,{api:async()=>{if(fail)throw Error('合成读取失败');return {enabled:true,items:[]};},esc,navigate(){},state});
 await view.refresh();assert.ok(h.innerHTML.includes('合成读取失败'));assert.ok(h.innerHTML.includes('合成公司'));
 fail=false;await view.refresh();assert.ok(!h.innerHTML.includes('合成读取失败'));assert.ok(!h.innerHTML.includes('合成公司'));
});
test('Leaving the page before a response prevents caching data for another account',async()=>{
 let active=true,resolve;const state={},h=host();mount(h,{api:()=>new Promise(r=>resolve=r),esc,navigate(){},active:()=>active,state});active=false;resolve({enabled:true,items:[item()]});await tick();assert.equal(state.value,undefined);assert.ok(!h.innerHTML.includes('合成公司'));
});
test('A refresh in flight prevents a settings write from racing an older response',async()=>{
 let resolve,writes=0;const state={value:{enabled:false,items:[]}},h=host();const view=mount(h,{api:(path,method)=>{if(method==='POST'){writes++;return Promise.resolve({});}return new Promise(r=>resolve=r);},esc,navigate(){},state});
 const pending=view.refresh();await h.querySelector('[data-reminder-toggle]').onclick();assert.equal(writes,0);resolve({enabled:false,items:[]});await pending;
});
test('a write survives Today redraw, locks the replacement controls and paints the latest state',async()=>{
 let release;const writes=[],state={value:{enabled:false,items:[]}},first=host(),second=host();
 const api=(path,method,body)=>{if(method==='POST'){writes.push(body.enabled);return writes.length===1?new Promise(resolve=>release=resolve):Promise.resolve({});}return Promise.resolve({enabled:writes.at(-1),items:[]});};
 const old=mount(first,{api,esc,navigate(){},state}),pending=first.querySelector('[data-reminder-toggle]').onclick();old.dispose();mount(second,{api,esc,navigate(){},state});
 assert.equal(second.querySelector('[data-reminder-toggle]').disabled,true);
 await second.querySelector('[data-reminder-toggle]').onclick();assert.deepEqual(writes,[true]);
 release({});await pending;
 assert.ok(second.innerHTML.includes('暂停变化提醒'));assert.equal(second.querySelector('[data-reminder-toggle]').disabled,false);
 await second.querySelector('[data-reminder-toggle]').onclick();assert.deepEqual(writes,[true,false]);
});
test('a failed write reports on the current host after redraw and remains retryable',async()=>{
 let reject;const state={value:{enabled:false,items:[item()]}},a=host(),b=host();let fail=true;
 const api=(path,method)=>method==='POST'?(fail?new Promise((_,r)=>reject=r):Promise.resolve({})):Promise.resolve({enabled:true,items:[]});
 const old=mount(a,{api,esc,navigate(){},state}),pending=a.querySelector('[data-reminder-toggle]').onclick();old.dispose();mount(b,{api,esc,navigate(){},state});
 reject(Error('合成保存失败'));await pending;assert.ok(b.innerHTML.includes('合成保存失败'));assert.ok(b.innerHTML.includes('合成公司'));
 fail=false;await b.querySelector('[data-reminder-toggle]').onclick();assert.ok(b.innerHTML.includes('暂停变化提醒'));assert.ok(!b.innerHTML.includes('合成保存失败'));
});
