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
const payload=jobs=>({version:'campustrace-chat-v4',candidate_hash:'reviewed-profile',exported_at:'2026-09-28T00:00:00Z',candidate:{revision:1,facts:[{id:'go',kind:'LANGUAGE',text:'Go'},{id:'queue',kind:'IMPLEMENTED',text:'实现任务队列'}]},preferences:{preferred_cities:['上海']},jobs});
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
    assert.equal(v.prompt_revision,C.promptRevision);assert.match(file.text,/不按满足条数计分/);
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

test('chat results accept full JSON or one fenced block, merge consistent files and reject ambiguity',()=>{
  const doc=id=>({version:'campustrace-chat-v3',candidate_hash:'profile',jobs:[{job_id:id,input_key:'input-'+id,requirements:[],matches:[]}]});
  assert.equal(C.parseDocuments(['说明\n```json\n'+JSON.stringify(doc('a'))+'\n```']).jobs[0].job_id,'a');
  assert.equal(C.parseDocuments([JSON.stringify(doc('a')),JSON.stringify(doc('b'))]).jobs.length,2);
  assert.throws(()=>C.parseDocuments([JSON.stringify(doc('a')),JSON.stringify(doc('a'))]),/重复/);
  assert.throws(()=>C.parseDocuments([JSON.stringify(doc('a')),JSON.stringify({...doc('b'),candidate_hash:'different'})]),/不同/);
  assert.throws(()=>C.parseDocuments(['```json\n{}\n```\n```json\n{}\n```']),/一个 JSON/);
  assert.throws(()=>C.parseDocuments(['{"version":"campustrace-chat-v2","jobs":[]}']),/新版/);
  assert.throws(()=>C.parseDocuments(['x'.repeat(2*1024*1024+1)]),/2 MB/);
});
test('new export instructions bind round-trip IDs and omit externally calculated scores from the wire result',()=>{
  const file=C.makeFiles({...payload([{...job(1),input_key:'literal-input-key'}]),version:'campustrace-chat-v3'})[0];
  assert.equal(data(file).jobs[0].input_key,'literal-input-key');
  assert.match(file.text,/完整阅读/);assert.match(file.text,/不按满足条数计分/);
  assert.match(C.instructions([file]),/导入聊天分析/);
});

test('merged chat jobs retain source positions locally without adding fields to the import document',()=>{
  const doc=ids=>({version:'campustrace-chat-v3',candidate_hash:'profile',jobs:ids.map(id=>({job_id:id,input_key:'input-'+id,requirements:[],matches:[]}))});
  const positions=[];
  const merged=C.parseDocuments([JSON.stringify(doc(['a','b','c'])),JSON.stringify(doc(['d']))],(...position)=>positions.push(position));
  assert.deepEqual(positions,[['a',0,1],['b',0,2],['c',0,3],['d',1,1]]);
  assert.deepEqual(merged,doc(['a','b','c','d']));
});


test('whole packages preserve project narratives and bind optional company comparison to its complete scope',()=>{
 const input={...payload([job(1),job(2)]),candidate:{facts:[],projects:[{id:'p',name:'队列',description:'完整背景与取舍',bullets:[{id:'b',text:'实现消费确认并保留失败重试'}]}]},company_inputs:[{company:'小红书',input_key:'scope',job_ids:['1','2']}]};
 const files=C.makeFiles(input);
 assert.deepEqual(data(files[0]).candidate,input.candidate);
 assert.equal(data(files[0]).company_inputs[0].input_key,'scope');
 const split=C.makeFiles(input,1);assert.equal(data(split[0]).company_inputs.length,0);
 assert.match(files[0].text,/不按技术名词拆成清单/);assert.match(files[0].text,/资料没写不等于不会/);
});
test('whole result schema and manual merge preserve assessments and company ranking without scores',()=>{
 const e=structuredClone(C.resultExample),j=e.jobs[0];
 assert.equal(e.version,'campustrace-chat-v4');assert.equal(e.prompt_revision,C.promptRevision);
 assert.ok(j.assessment);assert.ok(!('requirements' in j));assert.ok(!('score' in j.assessment));
 j.job_id='a';e.comparisons=[{company:'小红书',input_key:'scope',choices:[{job_id:'a',rank:1}]}];
 const b={...e,jobs:[{...j,job_id:'b'}],comparisons:[]};
 const merged=C.parseDocuments([JSON.stringify(e),JSON.stringify(b)]);
 assert.equal(merged.jobs.length,2);assert.equal(merged.comparisons.length,1);
 assert.throws(()=>C.parseDocuments([JSON.stringify(e),JSON.stringify({...b,version:'campustrace-chat-v3'})]),/不同/);
 assert.throws(()=>C.parseDocuments([JSON.stringify(e),JSON.stringify({...b,comparisons:e.comparisons})]),/比较重复/);
});

test('repair checklist points to each original file and exact requirement without re-exporting private facts',()=>{
 const doc={version:'campustrace-chat-v3',candidate_hash:'profile',jobs:[{job_id:'bad',requirements:[{id:'r7'}],matches:[{requirement_id:'r9'}]}]};
 const issues=[{job_id:'bad',company:'测试',title:'后端',diagnostic:{item_scope:'REQUIREMENT',item_index:1,validation_reason:'QUOTE'}},{job_id:'bad',company:'测试',title:'后端',diagnostic:{item_scope:'MATCH',item_index:1,validation_reason:'MATCH'}}];
 const report=C.repairInstructions(issues,doc,new Map([['bad',{name:'002.json',index:4}]]),d=>'修正 '+d.validation_reason);
 assert.match(report,/requirement_id=r7/);assert.match(report,/requirement_id=r9/);assert.match(report,/002.json，文件内第 4 个岗位/);
 assert.match(report,/candidate_hash=profile/);assert.match(report,/不能更换 input_key/);assert.match(report,/不重新生成已经导入/);
});
