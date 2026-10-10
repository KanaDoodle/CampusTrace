'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const D=require('./display.js');

function harness({catalogCount=3,missingID=false,withReport=true,candidateJobs=null,stored=new Map(),discard=true}={}){
 const elements=new Map(),reads=[],exports=[],downloads=[],blobs=new Map(),chatCalls=[];let html='',guard,blockedRead=false,capReads=0,userID='synthetic-account',chatSaved=null;const navigations=[],controls=[];
 const decode=v=>String(v).replaceAll('&quot;','"').replaceAll('&#39;',"'").replaceAll('&lt;','<').replaceAll('&gt;','>').replaceAll('&amp;','&');
 const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
 const catalog=candidateJobs||Array.from({length:catalogCount},(_,i)=>({id:i===0?'a':i===1?'b':'extra-'+i,title:'测试岗位 '+i,locations:['上海']}));
 if(missingID)catalog.splice(1,1);
 const set=value=>{
  html=value;elements.clear();controls.length=0;
  for(const m of value.matchAll(/<([a-z]+)\b([^>]*\bid="([^"]+)"[^>]*)>/gi))elements.set(m[3],{id:m[3],disabled:/\sdisabled(?:\s|$)/.test(m[2]),open:/\sopen(?:\s|$)/.test(m[2]),focus(){this.focused=true;},scrollIntoView(){this.scrolled=true;},replaceChildren(){this.cleared=true;},value:decode(m[2].match(/\bvalue="([^"]*)"/)?.[1]||''),checked:/\schecked(?:\s|$)/.test(m[2])});
  for(const m of value.matchAll(/<select\b[^>]*id="([^"]+)"[^>]*>([\s\S]*?)<\/select>/gi)){
   const options=[...m[2].matchAll(/<option\b([^>]*)>/g)],chosen=options.find(o=>/\bselected\b/.test(o[1]))||options[0];elements.get(m[1]).value=decode(chosen?.[1].match(/value="([^"]*)"/)?.[1]||'');
  }
  for(const m of value.matchAll(/<(?:input|button)\b([^>]*data-company-(select|remove|detail|prepare)="([^"]+)"[^>]*)>/g))controls.push({dataset:{['company'+m[2][0].toUpperCase()+m[2].slice(1)]:decode(m[3])},checked:/\schecked(?:\s|$)/.test(m[1]),focus(){},disabled:/\sdisabled(?:\s|$)/.test(m[1])});
  for(const m of value.matchAll(/<textarea[^>]*id="([^"]+)"[^>]*>([\s\S]*?)<\/textarea>/g))elements.get(m[1]).value=decode(m[2]);
  const formHTML=value.match(/<form id="company-eval-form">([\s\S]*?)<\/form>/)?.[1];
  if(formHTML){
   const options=[...formHTML.matchAll(/<option\b([^>]*)>/g)],choice=options.find(m=>/\bselected\b/.test(m[1]))||options[0];
   const top={value:decode(choice?.[1].match(/value="([^"]*)"/)?.[1]||''),focus(){this.focused=true;}},reason={value:decode(formHTML.match(/<textarea[^>]*>([\s\S]*?)<\/textarea>/)?.[1]||'')},reviewed={checked:/name="reviewed"[^>]*\bchecked/.test(formHTML)};
   const fields=Object.assign([top,reason,reviewed],{top,reason,reviewed}),form=elements.get('company-eval-form');form.elements=fields;form.reportValidity=()=>!!top.value&&!!reason.value.trim()&&reviewed.checked;
  }
  return true;
 };
 const document={querySelector:s=>s==='#company-eval-form select'?elements.get('company-eval-form')?.elements.top:elements.get(s.slice(1)),querySelectorAll:s=>{const key=s.match(/\[data-company-([^\]]+)\]/)?.[1];return key?controls.filter(c=>Object.hasOwn(c.dataset,'company'+key[0].toUpperCase()+key.slice(1))):[];},body:{append(){}},createElement:()=>({click(){downloads.push({filename:this.download,blob:blobs.get(this.href)});},remove(){}})};
 const context={document,Blob,TextEncoder,sessionStorage:{getItem:k=>stored.get(k),setItem:(k,v)=>stored.set(k,v)},URL:{createObjectURL:blob=>{const id='blob:fixture-'+blobs.size;blobs.set(id,blob);return id;},revokeObjectURL(){}},setTimeout(){},CampusModels:{bindUser(){}},CampusMatching:{bindUser(){},matchIdentity:()=>({model_name:'fixture-model',mask_name:''})},CampusNavigation:{register:v=>guard=v,leave:async()=>true,confirmDiscard:async()=>discard},console};
 vm.createContext(context);for(const file of ['matching_chat.js','applications.js','campaigns.js','matching_decision.js','company_chat.js','company_decision.js'])vm.runInContext(fs.readFileSync(__dirname+'/'+file,'utf8'),context);
 const api=async(path,method,body)=>{
  if(path==='/api/profile/resume/capabilities'){capReads++;return {user_id:userID};}
  if(path==='/api/matching/company-catalog')return catalog.length?[{company:'测试公司',total:catalog.length}]:[];
  if(path==='/api/matching/company-candidates')return structuredClone(catalog.map(j=>({job:j,state:j.state||'BASIC',fit:j.fit||'',score:j.score??null,preliminary_score:j.preliminary_score||0,excluded_reason:j.excluded_reason||'',campaign:j.campaign,application:j.application})));
  if(path.startsWith('/api/matching/company-catalog?'))return structuredClone(catalog);
  if(path==='/api/matching/company-workspace'){
   reads.push(structuredClone(body));if(blockedRead)throw Error('读取失败');
   const jobs=catalog.filter(j=>!body.job_ids.length||body.job_ids.includes(j.id)),ids=jobs.map(j=>j.id);
   return {comparison:{company:'测试公司',scope:body.job_ids.length?'SELECTED':'ALL',total:jobs.length,pending:0,stale:0,holistic_job_ids:ids,holistic_input_key:'current-scope',holistic:chatSaved||(withReport?{summary:'合成比较结果',choices:ids.map((id,i)=>({job_id:id,rank:i+1,reason:'合成比较',advantage:'合成优势',tradeoff:'合成取舍',job_excerpt:'合成原文',evidence:[]}))}:null),jobs:jobs.map(job=>({job,state:'BASIC'}))},workflow:{jobs:[],campaigns:[],applications:[]}};
  }
  if(path==='/api/matching/evaluation/export'){
   exports.push(structuredClone(body));return {version:'campustrace-matching-eval-v1',cases:[{id:'e'.repeat(64),candidate:{document:'合成脱敏材料'},jobs:body.job_ids.map(job_id=>({job_id})),reference:structuredClone(body.reference)}],recorded_results:withReport?[{report:{summary:'合成比较结果'}}]:[]};
  }
  if(path.startsWith('/api/matching/company-chat/')){
   chatCalls.push({path,body:structuredClone(body)});
   if(path.endsWith('/export'))return {document:{version:'campustrace-company-chat-v1',prompt_revision:'fixture-revision',candidate_hash:'c'.repeat(64),company:'测试公司',input_key:'i'.repeat(64),job_ids:body.job_ids,summary:'',choices:body.job_ids.map(job_id=>({job_id,rank:0,reason:'',advantage:'',tradeoff:'',job_excerpt:'',evidence:[]})),questions:[]},candidate:{document:'合成脱敏资料'},jobs:body.job_ids.map(job_id=>({job_id,title:catalog.find(j=>j.id===job_id).title,text:'合成岗位原文'})),prompt:'完整阅读，不按技术名词数量计分。',exported_at:'2026-10-09T06:00:00Z'};
   if(path.endsWith('/preview'))return {report:{...body.document,model:'manual-chat\nChatGPT 聊天导入'},jobs:body.document.job_ids.map(job_id=>({job_id,title:catalog.find(j=>j.id===job_id).title})),preview_key:'reviewed-preview',replaces:false};
   if(path.endsWith('/confirm')){assert.equal(body.preview_key,'reviewed-preview');chatSaved={...body.document,model:'manual-chat\nChatGPT 聊天导入'};return {imported:body.document.choices.length};}
  }
  throw Error('Unexpected API: '+path);
 };
 return {elements,reads,exports,downloads,stored,chatCalls,navigations,controls,capReads:()=>capReads,user:id=>userID=id,html:()=>html,dirty:()=>guard.dirty(),blockRead:()=>blockedRead=true,start:initial=>context.CampusCompanyDecision.page(set,'<h1>公司投递决策</h1>',{api,esc,D,navigate:(name,query)=>navigations.push({name,query}),initialCompany:'测试公司',...initial}),submit:()=>elements.get('company-eval-form').onsubmit({preventDefault(){},target:elements.get('company-eval-form')})};
}

test('company picker reveals temporarily hidden jobs without clearing a previously selected scope',async()=>{
 const h=harness({candidateJobs:[{id:'a',title:'已满批次的候选',locations:[],campaign:{hide_unsubmitted:true,limit:1,submitted:1}},{id:'b',title:'独立批次',locations:[]}]});
 await h.start({initialIDs:['a']});
 assert.ok(!h.controls.some(c=>c.dataset.companySelect==='a'));assert.match(h.html(),/已收起 1 个限投已满岗位/);assert.match(h.html(),/已选 1 \/ 16/);
 h.elements.get('company-picker-quota').onchange({target:{checked:true,value:''}});
 assert.match(h.elements.get('company-picker-content').innerHTML,/data-company-select="a"/);assert.match(h.elements.get('company-picker-content').innerHTML,/本批已投满 1\/1/);
 h.elements.get('company-picker-quota').onchange({target:{checked:false,value:''}});
 assert.doesNotMatch(h.elements.get('company-picker-content').innerHTML,/data-company-select="a"/);
 h.elements.get('company-picker-selected').onchange({target:{checked:true,value:''}});
 assert.match(h.elements.get('company-picker-content').innerHTML,/data-company-select="a"/);
 assert.equal(h.reads.length,1);assert.equal(h.exports.length,0);assert.equal(h.chatCalls.length,0);
});

test('visible export entry opens and focuses the form without calling a model or preparing a download',async()=>{
 const h=harness();await h.start({initialIDs:['a','b']});
 assert.equal(h.elements.get('company-open-evaluation').disabled,false);assert.equal(h.elements.get('company-evaluation').open,false);
 h.elements.get('company-open-evaluation').onclick();
 assert.equal(h.elements.get('company-evaluation').open,true);assert.equal(h.elements.get('company-evaluation').scrolled,true);assert.equal(h.elements.get('company-eval-form').elements.top.focused,true);
 assert.equal(h.elements.get('company-eval-form').elements.top.value,'');assert.equal(h.exports.length,0);assert.equal(h.downloads.length,0);
 await h.submit();assert.equal(h.exports.length,0,'human reference is required');
});

test('handoff preserves the exact selected scope even when the company has more than 200 jobs',async()=>{
 const h=harness({catalogCount:205});await h.start({initialIDs:['b','a','b'],initialEvaluation:true});
 assert.equal(h.reads.length,1);assert.deepEqual(h.reads[0].job_ids,['b','a']);assert.equal(h.elements.get('company-evaluation').open,true);
 assert.match(h.html(),/当前范围已有比较/);assert.match(h.html(),/本次手动选择的岗位 2 个/);
});

test('missing handoff jobs never expand an evaluation to the full company',async()=>{
 const h=harness({missingID:true});await h.start({initialIDs:['a','b'],initialEvaluation:true});
 assert.equal(h.reads.length,0);assert.equal(h.exports.length,0);assert.match(h.html(),/本次比较中有岗位已不在/);
 assert.equal(h.elements.get('company-open-evaluation').disabled,true);
});

test('JSON download contains the independent human annotation and current recorded result and marks edits saved',async()=>{
 const h=harness();await h.start({initialIDs:['a','b'],initialEvaluation:true});
 const fields=h.elements.get('company-eval-form').elements;fields.top.value='b';fields.reason.value='我更认可第二份岗位的核心工作';fields.reviewed.checked=true;
 assert.equal(h.dirty(),true);await h.submit();
 assert.equal(h.exports.length,1);assert.deepEqual(h.exports[0].job_ids,['a','b']);assert.equal(h.exports[0].expected_scope_key,'current-scope');assert.deepEqual(h.exports[0].reference.acceptable_top_job_ids,['b']);
 assert.equal(h.downloads.length,0,'preview must not immediately download');assert.equal(h.elements.get('company-eval-download').disabled,false);
 h.elements.get('company-eval-download').onclick();
 assert.equal(h.downloads.length,1);assert.match(h.downloads[0].filename,/^CampusTrace-匹配评测-.+\.json$/);
 const payload=JSON.parse(await h.downloads[0].blob.text());assert.equal(payload.recorded_results.length,1);assert.deepEqual(payload.cases[0].reference.acceptable_top_job_ids,['b']);assert.equal(h.dirty(),false);
});

test('missing reports are clearly distinguished and a failed refresh disables stale export',async()=>{
 const h=harness({withReport:false});await h.start({initialIDs:['a','b']});assert.match(h.html(),/当前范围尚无有效比较/);
 h.blockRead();await h.elements.get('company-refresh').onclick();assert.equal(h.elements.get('company-open-evaluation').disabled,true);assert.match(h.html(),/上次读取的报告/);
});

test('score filters and quick picking retain existing choices and share them with radar',async()=>{
 const h=harness({candidateJobs:[{id:'a',title:'普通后端',locations:['上海'],state:'ANALYZED',fit:'RELATED'},{id:'b',title:'优先后端',locations:['上海'],state:'ANALYZED',fit:'STRONG'},{id:'c',title:'旧分析',locations:['上海'],state:'STALE',fit:'STRONG',score:99},{id:'closed',title:'已关闭',locations:['上海'],state:'ANALYZED',fit:'STRONG',current_status:'CLOSED',excluded_reason:'岗位已关闭'}]});
 await h.start({initialIDs:['a']});
 h.elements.get('company-picker-state').onchange({target:{value:'ANALYZED'}});
 h.elements.get('company-picker-fit').onchange({target:{value:'RELEVANT'}});
 h.elements.get('company-quick-count').value='4';h.elements.get('company-select-top').onclick();
 const key='campustrace:match-selection:v1:synthetic-account';assert.deepEqual(JSON.parse(h.stored.get(key)).sort(),['a','b']);
 await h.elements.get('company-apply-scope').onclick();assert.deepEqual(h.reads.at(-1).job_ids.sort(),['a','b']);
 h.elements.get('company-list-view').onclick();assert.deepEqual(JSON.parse(JSON.stringify(h.navigations.at(-1))),{name:'matching',query:{view:'list'}});
 await h.start();assert.deepEqual(h.reads.at(-1).job_ids.sort(),['a','b']);assert.equal(h.elements.get('company-picker-state').value,'ANALYZED');assert.equal(h.elements.get('company-picker-fit').value,'RELEVANT');
 assert.equal(h.exports.length,0,'picking never calls a model or exports personal materials');
});

test('hidden selections survive searches and clearing the selection does not broaden a saved report scope',async()=>{
 const h=harness();await h.start({initialIDs:['a','b']});
 h.elements.get('company-picker-search').oninput({target:{value:'测试岗位 2'}});
 assert.deepEqual(JSON.parse(h.stored.get('campustrace:match-selection:v1:synthetic-account')),['a','b']);
 h.elements.get('company-clear-selection').onclick();assert.deepEqual(JSON.parse(h.stored.get('campustrace:match-selection:v1:synthetic-account')),[]);
 await h.start();assert.deepEqual(h.reads.at(-1).job_ids,['a','b']);assert.equal(h.elements.get('company-picker-search').value,'测试岗位 2');
 assert.equal(h.elements.get('company-apply-scope').disabled,true);
});

test('company choices do not overwrite selected jobs from another company or account',async()=>{
 const stored=new Map([['campustrace:match-selection:v1:synthetic-account',JSON.stringify(['other-employer'])]]),h=harness({stored});await h.start({initialIDs:['a']});
 assert.deepEqual(JSON.parse(stored.get('campustrace:match-selection:v1:synthetic-account')),['other-employer','a']);
 h.user('second-account');await h.start();assert.deepEqual(h.reads.at(-1).job_ids,[]);assert.deepEqual(JSON.parse(stored.get('campustrace:match-selection:v1:second-account')),[]);
 assert.deepEqual(JSON.parse(stored.get('campustrace:match-selection:v1:synthetic-account')),['other-employer','a']);
});

test('quick picking is additive and stops at the company comparison limit',async()=>{
 const h=harness({catalogCount:45});await h.start({initialIDs:['a','b']});h.elements.get('company-quick-count').value='16';h.elements.get('company-select-top').onclick();
 const ids=JSON.parse(h.stored.get('campustrace:match-selection:v1:synthetic-account'));assert.equal(ids.length,16);assert.ok(ids.includes('a')&&ids.includes('b'));
});

test('large company entry waits for a candidate scope before reading full reports',async()=>{
 const h=harness({catalogCount:45});await h.start();
 assert.equal(h.reads.length,0,'entry only needs compact candidate summaries');
 h.elements.get('company-quick-count').value='4';h.elements.get('company-select-top').onclick();
 await h.elements.get('company-apply-scope').onclick();
 assert.equal(h.reads.length,1);assert.equal(h.reads[0].job_ids.length,4);
});

test('an empty company workspace returns explicitly to the radar list',async()=>{
 const h=harness({catalogCount:0});await h.start();
 h.elements.get('company-empty').onclick();
 assert.deepEqual(JSON.parse(JSON.stringify(h.navigations.at(-1))),{name:'matching',query:{view:'list'}});
});

test('canceling a scope change does not open analysis for the old selection',async()=>{
 const h=harness({discard:false});await h.start({initialIDs:['a']});
 h.elements.get('company-eval-form').elements.reason.value='尚未保存的人工判断';
 const added=h.controls.find(c=>c.dataset.companySelect==='b');added.checked=true;added.onchange();
 await h.elements.get('company-analyze').onclick();
 assert.equal(h.reads.length,1);assert.equal(h.navigations.length,0);
 assert.equal(h.dirty(),true);
});

test('company chat export previews full data before download and keeps manual evaluation independent',async()=>{
 const h=harness({withReport:false});await h.start({initialIDs:['b','a']});
 await h.elements.get('company-open-chat').onclick();assert.equal(h.elements.get('company-chat').open,true);
 await h.elements.get('company-chat-export').onclick();assert.equal(h.chatCalls.length,1);assert.equal(h.downloads.length,0);
 assert.deepEqual(h.chatCalls[0].body.job_ids,['a','b']);assert.match(h.html(),/查看实际导出的完整文字/);
 await h.elements.get('company-chat-download').onclick();assert.equal(h.downloads.length,1);assert.match(h.downloads[0].filename,/公司比较/);
 const text=await h.downloads[0].blob.text();assert.match(text,/合成脱敏资料/);assert.match(text,/result_template/);assert.equal(h.exports.length,0);
});

test('company chat import requires preview, persists its scope and returns to the company report without clearing selection',async()=>{
 const h=harness({withReport:false});await h.start({initialIDs:['a','b']});
 assert.equal(h.elements.has('company-chat-confirm'),false);
 const doc={version:'campustrace-company-chat-v1',prompt_revision:'fixture-revision',candidate_hash:'c'.repeat(64),company:'测试公司',input_key:'i'.repeat(64),job_ids:['b','a'],summary:'完整材料下的新比较',choices:[{job_id:'a',rank:2,reason:'备选',advantage:'有基础',tradeoff:'领域差距',job_excerpt:'合成原文',evidence:[]},{job_id:'b',rank:1,reason:'首选',advantage:'核心工作贴近',tradeoff:'规模待确认',job_excerpt:'合成原文',evidence:[]}],questions:[]};
 const input=h.elements.get('company-chat-text');input.value=JSON.stringify(doc);input.oninput({target:input});assert.equal(h.dirty(),true);
 await h.elements.get('company-chat-preview').onclick();assert.equal(h.chatCalls.length,1);assert.match(h.html(),/核对 GPT 返回的排序/);assert.match(h.html(),/确认保存公司比较/);
 await h.elements.get('company-chat-confirm').onclick();assert.equal(h.chatCalls.length,2);assert.equal(h.chatCalls[1].body.preview_key,'reviewed-preview');assert.equal(h.dirty(),false);assert.match(h.html(),/完整材料下的新比较/);assert.match(h.html(),/GPT 聊天导入/);
 assert.deepEqual(h.reads.at(-1).job_ids,['b','a']);assert.deepEqual(JSON.parse(h.stored.get('campustrace:match-selection:v1:synthetic-account')),['b','a']);assert.equal(h.exports.length,0);
});

test('editing a returned comparison removes confirmation and editing redaction invalidates the download',async()=>{
 const h=harness();await h.start({initialIDs:['a','b']});await h.elements.get('company-chat-export').onclick();
 const input=h.elements.get('company-chat-mask');input.value='新姓名';input.oninput({target:input});assert.equal(h.elements.get('company-chat-download').disabled,true);await h.elements.get('company-chat-download').onclick();assert.equal(h.downloads.length,0);
});

test('importing a different scope does not discard an unsaved human preference when the user stays',async()=>{
 const h=harness({discard:false});await h.start({initialIDs:['a','b']});
 h.elements.get('company-eval-form').elements.reason.value='我尚未保存的人工理由';
 const doc={version:'campustrace-company-chat-v1',prompt_revision:'fixture-revision',candidate_hash:'c'.repeat(64),company:'测试公司',input_key:'i'.repeat(64),job_ids:['a'],summary:'另一范围',choices:[{job_id:'a',rank:1,reason:'相关',advantage:'基础相关',tradeoff:'待确认',job_excerpt:'原文',evidence:[]}],questions:[]};
 const input=h.elements.get('company-chat-text');input.value=JSON.stringify(doc);input.oninput({target:input});await h.elements.get('company-chat-preview').onclick();await h.elements.get('company-chat-confirm').onclick();
 assert.equal(h.chatCalls.length,1,'no confirmation write after cancellation');assert.equal(h.elements.get('company-eval-form').elements.reason.value,'我尚未保存的人工理由');assert.deepEqual(h.reads.at(-1).job_ids,['a','b']);assert.equal(h.dirty(),true);
});

test('quick selection size survives filtering and returning to the same company',async()=>{
 const h=harness();await h.start({initialIDs:['a']});
 h.elements.get('company-quick-count').onchange({target:{value:'8'}});
 h.elements.get('company-picker-search').oninput({target:{value:'测试'}});
 await h.start();
 assert.equal(h.elements.get('company-quick-count').value,'8');
 assert.equal(h.elements.get('company-picker-search').value,'测试');
 h.user('another-account');await h.start();
 assert.equal(h.elements.get('company-quick-count').value,'4');
});


test('entering company view from radar reuses freshly read capabilities',async()=>{
 const h=harness();await h.start({capabilities:{user_id:'synthetic-account'}});
 assert.equal(h.capReads(),0);assert.match(h.html(),/测试公司/);
 const normal=harness();await normal.start();assert.equal(normal.capReads(),1);
});
