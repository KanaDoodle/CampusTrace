const {test}=require('node:test');
const assert=require('node:assert/strict');
const C=require('./matching_chat.js');
const M=require('./matching.js');
const row=(id,state='BASIC')=>({job:{id,company:'小红书',title:'服务端开发'},state,text_bytes:1000,excluded_reason:''});
test('selection spans pages and filters, persists per account, and removes inaccessible jobs',()=>{
  const rows=Array.from({length:137},(_,i)=>row(String(i)));
  const selected=new Set();C.addSelection(selected,rows.slice(0,50));C.addSelection(selected,rows.slice(100,137));
  assert.equal(selected.size,87);
  assert.equal(C.selectedRows(M.filtered(rows,'小红书',''),selected).length,87);
  assert.equal(C.selectedRows(M.filtered(rows,'另一个公司',''),selected).length,0);
  assert.equal(selected.size,87);
  const saved=new Map();global.sessionStorage={getItem:k=>saved.get(k),setItem:(k,v)=>saved.set(k,v)};
  C.storeSelection('alice',selected);
  assert.equal(C.readSelection('alice',rows).size,87);assert.equal(C.readSelection('bob',rows).size,0);
  C.pruneSelection(selected,rows.slice(0,100));assert.equal(selected.size,50);
  assert.equal(C.readSelection('alice',rows.slice(0,100)).size,50);
});
test('bulk selected jobs still use the API round limit and skip cached or excluded jobs',()=>{
  const rows=Array.from({length:137},(_,i)=>row(String(i)));rows[0].state='ANALYZED';rows[1].excluded_reason='已关闭';
  const selected=new Set();C.addSelection(selected,rows);
  const jobs=M.shortlist(C.selectedRows(rows,selected),30);
  assert.equal(jobs.length,30);assert.equal(jobs[0].job.id,'2');assert.equal(jobs.at(-1).job.id,'31');
});
const payload=jobs=>({version:'campustrace-chat-v1',candidate_hash:'reviewed-profile',exported_at:'2026-09-28T00:00:00Z',candidate:{revision:1,facts:[{id:'go',kind:'LANGUAGE',text:'Go'},{id:'queue',kind:'IMPLEMENTED',text:'实现任务队列'}]},preferences:{preferred_cities:['上海']},jobs});
const job=i=>({job_id:String(i),company:'小红书',title:'服务端开发 '+i,text:'熟悉 Go，参与任务队列开发'});
function data(file){return JSON.parse(file.text.split('以下 JSON 为本包完整数据：\n')[1]);}
test('137 jobs split into complete self-contained packages with consistent facts and no lost or duplicated IDs',()=>{
  const input=payload(Array.from({length:137},(_,i)=>job(i))),files=C.makeFiles(input);
  assert.equal(files.length,18);
  const ids=[];
  for(const file of files){
    const v=data(file);assert.deepEqual(v.candidate,input.candidate);assert.deepEqual(v.preferences,input.preferences);
    assert.equal(v.candidate_hash,input.candidate_hash);assert.ok(v.jobs.length<=8);
    assert.ok(new TextEncoder().encode(file.text).length<=48000);ids.push(...v.jobs.map(j=>j.job_id));
    assert.ok(file.text.includes('confidence低于0.8'));assert.ok(file.text.includes('coverage'));
  }
  assert.deepEqual(ids,input.jobs.map(j=>j.job_id));assert.equal(new Set(ids).size,137);
});
test('UTF-8 text budget counts complete instructions and repeated profile; oversized job retains full text alone',()=>{
  const input=payload([job(1),{...job(2),text:'原文不能截断'.repeat(5000)},job(3)]),files=C.makeFiles(input);
  assert.equal(files.length,3);assert.equal(files[1].jobCount,1);assert.equal(files[1].oversized,true);
  assert.equal(data(files[1]).jobs[0].text,input.jobs[1].text);
  const small=C.makeFiles(payload(Array.from({length:8},(_,i)=>({...job(i),text:'中'.repeat(6000)}))));
  assert.ok(small.length>1);assert.ok(small.every(f=>new TextEncoder().encode(f.text).length<=48000));
});
test('a small company stays together even when mixed selections would cross a package boundary',()=>{
  const jobs=[...Array.from({length:5},(_,i)=>({...job(i),company:'甲公司'})),...Array.from({length:5},(_,i)=>({...job(i+5),company:'乙公司'}))];
  const files=C.makeFiles(payload([jobs[0],jobs[5],...jobs.slice(1,5),...jobs.slice(6)]));
  assert.equal(files.length,2);
  assert.deepEqual(data(files[0]).jobs.map(j=>j.job_id),['0','1','2','3','4']);
  assert.deepEqual(data(files[1]).jobs.map(j=>j.job_id),['5','6','7','8','9']);
});
test('download contains exact reviewed edits, with merge instructions for ZIP and plain Markdown for one package',async()=>{
  global.JSZip=require('./vendor/jszip.min.js');
  let blob,clicked=0,filename='';
  const oldURL=global.URL,oldDocument=global.document;
  global.URL={createObjectURL:b=>{blob=b;return 'blob:reviewed-export';},revokeObjectURL:()=>{}};
  global.document={body:{append:()=>{}},createElement:()=>({set download(value){filename=value;},click:()=>{clicked++;},remove:()=>{}})};
  try{
    const files=C.makeFiles(payload(Array.from({length:10},(_,i)=>job(i))));
    files[1].text+='\n人工核对备注：已删除敏感内容。\n';
    await C.download(files);
    assert.equal(clicked,1);assert.equal(filename,'CampusTrace-ChatGPT分析包.zip');
    const zip=await global.JSZip.loadAsync(await blob.arrayBuffer());
    assert.equal(await zip.file(files[1].name).async('string'),files[1].text);
    assert.ok((await zip.file('使用说明.txt').async('string')).includes('共 2 包、10 个岗位'));
    assert.equal(await zip.file('汇总指令.txt').async('string'),C.mergePrompt);
    await C.download([files[0]]);
    assert.equal(filename,files[0].name);assert.equal(await blob.text(),files[0].text);
  }finally{global.URL=oldURL;global.document=oldDocument;delete global.JSZip;}
});
