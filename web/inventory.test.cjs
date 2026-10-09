const {test}=require('node:test'),assert=require('node:assert/strict');
const I=require('./inventory.js');
const fields=['id','title','company','cities','input_key','state','preliminary_score','direction','application'];
const row=(id,score=50)=>[id,id,'合成公司',['上海'],'input-'+id,'BASIC',score,'MATCH',null];
test('global index supports selecting and sorting jobs beyond the hydrated page',()=>{
 const first=I.merge(null,{snapshot_key:'one',index:{fields,full:true,rows:[row('a'),row('b',80),row('c')]},jobs:[{job:{id:'a'},local:{reasons:['card']}}]});
 assert.equal(first.jobs.length,3);assert.equal(first.jobs[1].preliminary_score,80);assert.equal(first.jobs[1].card_pending,true);assert.equal(first.jobs[0].card_pending,undefined);
 const page=I.merge(first,{snapshot_key:'one',index:{fields,full:false},jobs:[{job:{id:'b'},preliminary_score:80}]});
 assert.equal(page.jobs.length,3);assert.equal(page.jobs[1].card_pending,undefined);assert.equal(page.jobs[0].local.reasons[0],'card');
});
test('delta replaces changed rows, removes inaccessible rows and invalidates old card explanations',()=>{
 const first=I.merge(null,{snapshot_key:'one',index:{fields,full:true,rows:[row('a'),row('b')]},jobs:[]});
 const next=I.merge(first,{snapshot_key:'two',index:{fields,full:false,upserts:[row('b',90),row('c')],removed:['a']},jobs:[]});
 assert.deepEqual(next.jobs.map(r=>r.job.id),['b','c']);assert.equal(next.jobs[0].preliminary_score,90);assert.equal(next.jobs[0].card_pending,true);assert.equal(first.jobs.length,2);
 const evicted=I.merge(next,{snapshot_key:'three',index:{fields,full:true,rows:[row('z')]},jobs:[{job:{id:'b'}}]});assert.deepEqual(evicted.jobs.map(r=>r.job.id),['z']);
});
