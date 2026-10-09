const {test}=require('node:test'),assert=require('node:assert/strict'),B=require('./knowledge_bulk.js');
const file=(name,text)=>({name,size:new TextEncoder().encode(text).length,text:async()=>text});
test('bulk file preview preserves text, flags duplicates and bounds file and body sizes',async()=>{
 const rows=await B.readFiles([file('Redis.txt','恢复 pending 消息'),file('Redis.md','恢复 pending 消息'),file('other.pdf','%PDF'),file('big.txt','x'.repeat(60001))]);assert.deepEqual(rows.map(v=>v.status),['ready','duplicate','invalid','invalid']);assert.equal(rows[0].text,'恢复 pending 消息');assert.equal(rows[1].selected,false);
 await assert.rejects(B.readFiles(Array.from({length:65},()=>file('x.txt','ok'))),/64/);await assert.rejects(B.readFiles(Array.from({length:20},()=>file('x.txt','a'.repeat(60000)))),/1 MB/);
});
test('partial imports retain successful results and retry only failed rows with the exact original input',async()=>{
 const queue={rows:await B.readFiles([file('one.txt','one'),file('two.md','two'),file('three.txt','three')])};let fail=true,calls=[];
 const api=async(path,method,body)=>{assert.equal(path,'/api/documents/import');calls.push(body.title);if(body.title==='two'&&fail)throw Error('lost response');return {id:body.title,reused:body.title==='three'||body.title==='two'};};
 await B.run(queue,{api});assert.deepEqual(queue.rows.map(v=>v.status),['imported','failed','reused']);fail=false;await B.run(queue,{api,retry:true});assert.deepEqual(calls,['one','two','three','two']);assert.equal(queue.rows[1].status,'reused');
});
test('navigation and explicit stop prevent further imports after the in-flight item completes',async()=>{
 const queue={rows:await B.readFiles([file('one.txt','one'),file('two.md','two')])};let alive=true,calls=0;await B.run(queue,{active:()=>alive,api:async()=>{calls++;alive=false;return {id:'1'};}});assert.equal(calls,1);assert.equal(queue.rows[1].status,'ready');assert.equal(queue.busy,false);
});
