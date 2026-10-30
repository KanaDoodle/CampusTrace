const {test}=require('node:test');const assert=require('node:assert/strict');const {create}=require('./job_search.js');
const tick=()=>new Promise(r=>setImmediate(r));
test('a late response cannot replace a newer query or inventory scope',async()=>{
 const pending=[];let changes=0;const search=create({api:(...args)=>new Promise(resolve=>pending.push({args,resolve})),changed:()=>changes++});
 search.ensure({scope:'alice:v1',text:'C++'});await tick();search.ensure({scope:'alice:v2',text:'Go'});await tick();
 pending[1].resolve({job_ids:['new'],mode:'fulltext'});await tick();pending[0].resolve({job_ids:['old'],mode:'fulltext'});await tick();
 const state=search.ensure({scope:'alice:v2',text:'Go'});assert.deepEqual([...state.ids],['new']);assert.equal(changes,1);assert.equal(pending.length,2);
 search.ensure({scope:'alice:v2',text:''});assert.equal(search.ensure({scope:'alice:v2',text:''}),null);
});
test('a departed page ignores results; errors do not start an automatic request loop',async()=>{
 let active=true,resolve,calls=0,changes=0;const s=create({active:()=>active,api:()=>{calls++;return new Promise(r=>resolve=r)},changed:()=>changes++});
 s.ensure({scope:'alice',text:'Go'});await tick();active=false;resolve({job_ids:['x'],mode:'fulltext'});await tick();assert.equal(changes,0);
 const broken=create({api:()=>{calls++;return Promise.reject(Error('offline'))}});broken.ensure({scope:'bob',text:'C#'});await tick();const count=calls;
 assert.equal(broken.ensure({scope:'bob',text:'C#'}).error,true);await tick();assert.equal(calls,count);
});
test('clearing a query invalidates in-flight results and preserves no search data',async()=>{
 let resolve,changes=0;const s=create({api:()=>new Promise(r=>resolve=r),changed:()=>changes++});s.ensure({scope:'one',text:'Redis'});await tick();s.ensure({scope:'one'});resolve({job_ids:['private'],mode:'basic',more:true});await tick();assert.equal(changes,0);assert.equal(s.ensure({scope:'two'}),null);
});
test('explicit retry recovers a failed query once and keeps render retries inert',async()=>{
 let calls=0,resolve;const s=create({api:()=>{calls++;if(calls===1)return Promise.reject(Error('SEARCH_BUSY'));return new Promise(r=>resolve=r)}});
 const query={scope:'same-account',text:'Redis',mode:'ALL',exclude:'实习'};
 s.ensure(query);await tick();assert.equal(s.ensure(query).error,true);
 for(let i=0;i<10;i++)s.ensure(query);await tick();assert.equal(calls,1);
 assert.equal(s.retry(),true);assert.equal(s.ensure(query).pending,true);
 assert.equal(s.retry(),false);s.ensure(query);await tick();assert.equal(calls,2);
 resolve({job_ids:['recovered'],mode:'fulltext'});await tick();
 assert.equal(s.ensure(query).error,false);assert.equal(s.ensure(query).ids.has('recovered'),true);
 assert.equal(s.retry(),false);assert.equal(calls,2);
});
