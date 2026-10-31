const {test}=require('node:test'),assert=require('node:assert/strict');
const {mount}=require('./recruiting_reminders.js'),{esc}=require('./ui.js');
function host(){
 let html='',nodes=[];const details={open:false};
 return {get innerHTML(){return html;},set innerHTML(v){html=v;nodes=[...v.matchAll(/<button[^>]*\b(data-reminder-[a-z]+)(?:="([^"]*)")?[^>]*>/g)].map(m=>({attribute:m[1],dataset:{[m[1].slice(5).replace(/-([a-z])/g,(_,c)=>c.toUpperCase())]:m[2]},disabled:false}));},querySelector(s){return s==='details'?details:nodes.find(n=>s==='['+n.attribute+']')||null;},querySelectorAll(s){return s==='button'?nodes:nodes.filter(n=>s==='['+n.attribute+']');}};
}
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const item=(id='a'.repeat(32))=>({id,job_id:'b'.repeat(32),company:'合成公司',title:'<img onerror=alert(1)>',explanation:'岗位资料已变化'});
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
