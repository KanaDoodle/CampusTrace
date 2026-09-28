const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');

function harness({pending=['pending-a','pending-b'],failed=['failed-a','failed-b'],analyze,exportError,compare}={}){
  const stored=new Map(),elements=new Map(),requests=[],exports=[],decisionRequests=[];
  const model={url:'https://model.example/chat',model:'test-model',api_key:'synthetic-test-key'};
  const modelKey=JSON.stringify([model.url,model.model,'server-model']);
  stored.set('campustrace:match-progress:v1:alice',JSON.stringify({hash:'profile',model:modelKey,pending,failed:failed.map(id=>({id,message:'上次连接失败'})),done:1,calls:1}));
  const jobs=[...new Set([...pending,...failed])].map(id=>({job:{id,title:id,company:'测试公司',locations:[]},state:'BASIC',preliminary_score:50,text_bytes:1000,input_key:id,excluded_reason:''}));
  const snapshot={jobs,candidate:{facts:[]},candidate_hash:'profile',calls_today:0,settings:{round_limit:30,daily_calls:40,auto_new:false}};
  const document={activeElement:null,querySelector:selector=>elements.get(selector.slice(1)),querySelectorAll:()=>[],getElementById:id=>elements.get(id)};
  let html='';
  const set=value=>{
    html=value;elements.clear();
    for(const match of value.matchAll(/<([a-z][\w:-]*)\b([^>]*\bid="([^"]+)"[^>]*)>/gi)){
      const [,tag,attributes,id]=match;
      elements.set(id,{tagName:tag.toUpperCase(),disabled:/\sdisabled(?:\s|>|$)/.test(attributes),checked:/\schecked(?:\s|>|$)/.test(attributes),isConnected:true,innerHTML:'',dataset:{},getClientRects:()=>[],showModal(){this.open=true;},close(){this.open=false;this.onclose?.();}});
    }
    return true;
  };
  const context={document,setTimeout:()=>0,sessionStorage:{getItem:k=>stored.get(k),setItem:(k,v)=>stored.set(k,v)},CampusModels:{bindUser(){},requestConfig:()=>model,available:()=>true,label:()=> '测试模型'},CampusMatchingChat:{readSelection:()=>new Set(),selectedRows:()=>[],pruneSelection(){},storeSelection(){}},console};
  vm.createContext(context);vm.runInContext(fs.readFileSync(__dirname+'/ui.js','utf8'),context);vm.runInContext(fs.readFileSync(__dirname+'/matching_decision.js','utf8'),context);vm.runInContext(fs.readFileSync(__dirname+'/matching.js','utf8'),context);
  const api=async(path,method,body)=>{
    if(path==='/api/profile/resume/capabilities')return {user_id:'alice',model:'server-model',model_available:true};
    if(path==='/api/matching/preview')return structuredClone(snapshot);
    if(path==='/api/matching/company'){decisionRequests.push(body);if(compare)return compare(body);return {company:body.company,scope:body.scope,total:jobs.length,analyzed:0,pending:jobs.length,stale:0,recommendation:'NONE',reasons:['待分析不用于推荐'],jobs:[]};}
    if(path==='/api/matching/export'){
      exports.push(body);if(exportError)throw new Error(exportError);
      return {candidate_hash:snapshot.candidate_hash,candidate:snapshot.candidate,jobs:body.job_ids.map(id=>({job_id:id,title:id,text:'完整岗位文字 '+id}))};
    }
    if(path==='/api/matching/analyze'){
      requests.push([...body.job_ids]);
      snapshot.calls_today++;
      if(analyze)await analyze({ids:body.job_ids,elements});
      for(const job of snapshot.jobs)if(body.job_ids.includes(job.job.id))job.state='ANALYZED';
      return {analyzed:body.job_ids,reused:[],calls:1};
    }
    throw new Error('Unexpected local test API path: '+path);
  };
  const esc=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  return {start:()=>context.CampusMatching.page(set,'<h2>岗位匹配</h2>',{api,esc,D:{text:s=>s,date:()=>'',label:(_kind,s)=>s},active:()=>true,navigate(){}}),elements,requests,exports,decisionRequests,stored,snapshot,review:()=>elements.get('match-confirm').onclick(),html:()=>html,consent:()=>elements.get('match-consent').onchange({target:{checked:true}}),progress:()=>JSON.parse(stored.get('campustrace:match-progress:v1:alice'))};
}

test('saved work opens a full outbound review before consent and an explicit start',async()=>{
  const h=harness();await h.start();
  assert.equal(h.elements.get('match-continue').disabled,false);
  assert.equal(h.elements.get('match-retry').disabled,false);
  assert.deepEqual(h.requests,[]);
  await h.elements.get('match-continue').onclick();
  assert.equal(h.elements.get('match-review-dialog').open,true);
  assert.ok(h.html().includes('完整岗位文字 pending-a'));
  assert.equal(h.elements.get('match-confirm').disabled,true);
  assert.deepEqual(h.requests,[],'preparing complete outbound text makes no model request');
  h.consent();
  assert.equal(h.elements.get('match-confirm').disabled,false);
  assert.deepEqual(h.requests,[],'consent alone never starts paid analysis');
});

test('retry analyzes failed jobs individually, preserves pending work and accumulated counters',async()=>{
  const h=harness();await h.start();await h.elements.get('match-retry').onclick();h.consent();await h.review();
  assert.deepEqual(h.requests,[['failed-a'],['failed-b']]);
  assert.deepEqual(h.progress().pending,['pending-a','pending-b']);
  assert.deepEqual(h.progress().failed,[]);
  assert.equal(h.progress().done,3);assert.equal(h.progress().calls,3);
  assert.equal(h.elements.get('match-continue').disabled,false);
  assert.equal(h.elements.get('match-retry').disabled,true);
  assert.ok(!JSON.stringify(h.progress()).includes('synthetic-test-key'));
});

test('continuing unfinished work preserves failure items for a separate retry',async()=>{
  const h=harness();await h.start();await h.elements.get('match-continue').onclick();h.consent();await h.review();
  assert.deepEqual(h.requests,[['pending-a','pending-b']]);
  assert.deepEqual(h.progress().pending,[]);
  assert.deepEqual(h.progress().failed.map(f=>f.id),['failed-a','failed-b']);
  assert.equal(h.elements.get('match-retry').disabled,false);
});

test('pausing a retry keeps both unattempted retry jobs and the original unfinished queue',async()=>{
  const h=harness({analyze:async({elements})=>elements.get('match-pause').onclick()});await h.start();await h.elements.get('match-retry').onclick();h.consent();await h.review();
  assert.deepEqual(h.requests,[['failed-a']]);
  assert.deepEqual(h.progress().pending,['failed-b','pending-a','pending-b']);
  assert.equal(h.elements.get('match-continue').disabled,false);
});

test('failed retries retain their errors without losing or automatically running other pending work',async()=>{
  const h=harness({analyze:async()=>{throw Object.assign(new Error('模拟连接中断'),{code:'MODEL_CONNECTION_FAILED'});}});await h.start();await h.elements.get('match-retry').onclick();h.consent();await h.review();
  assert.deepEqual(h.requests,[['failed-a'],['failed-b']]);
  assert.deepEqual(h.progress().pending,['pending-a','pending-b']);
  assert.deepEqual(h.progress().failed.map(f=>f.id),['failed-a','failed-b']);
  assert.equal(h.progress().calls,3,'failed model attempts remain counted');
});

test('refresh removes deleted jobs and completed results from saved progress without an external request',async()=>{
  const h=harness();h.snapshot.jobs=h.snapshot.jobs.filter(j=>j.job.id!=='failed-a');h.snapshot.jobs[0].state='ANALYZED';await h.start();
  assert.deepEqual(h.progress().pending,['pending-b']);
  assert.deepEqual(h.progress().failed.map(f=>f.id),['failed-b']);
  assert.equal(h.progress().done,2);
  assert.deepEqual(h.requests,[]);
});

test('empty queues stay out of the workspace while daily limits explain disabled actions',async()=>{
  const empty=harness({pending:[],failed:[]});await empty.start();
  assert.equal(empty.elements.has('match-continue'),false);
  assert.equal(empty.elements.has('match-retry'),false);
  const limited=harness();limited.snapshot.calls_today=40;await limited.start();
  assert.equal(limited.elements.get('match-continue').disabled,true);
  assert.equal(limited.elements.get('match-retry').disabled,true);
  assert.ok(limited.html().includes('今日调用已达到上限'));
});

test('canceling an already consented review clears authorization without losing saved work',async()=>{
  const h=harness();await h.start();await h.elements.get('match-continue').onclick();h.consent();
  h.elements.get('match-review-dialog').close();
  await h.review();
  assert.deepEqual(h.requests,[]);
  assert.deepEqual(h.progress().pending,['pending-a','pending-b']);
  await h.elements.get('match-continue').onclick();
  assert.equal(h.elements.get('match-confirm').disabled,true);
});

for(const kind of ['candidate','job'])test(`${kind} changes after review require fresh consent before a model request`,async()=>{
  const h=harness();await h.start();await h.elements.get('match-continue').onclick();h.consent();
  if(kind==='candidate')h.snapshot.candidate_hash='updated-profile';else h.snapshot.jobs[0].input_key='updated-job';
  await h.review();
  assert.deepEqual(h.requests,[]);
  assert.equal(h.exports.length,2);
  assert.equal(h.elements.get('match-confirm').disabled,true);
});

test('failed local preparation never permits an external analysis request',async()=>{
  const h=harness({exportError:'岗位原文暂不可用'});await h.start();await h.elements.get('match-continue').onclick();
  assert.ok(h.html().includes('岗位原文暂不可用'));
  assert.equal(h.elements.get('match-confirm').disabled,true);
  h.consent();await h.review();
  assert.deepEqual(h.requests,[]);
});


test('company comparison is an explicit local read without keys, outbound consent or model requests',async()=>{
  const h=harness();await h.start();h.elements.get('match-open-comparison').onclick();
  assert.equal(h.decisionRequests.length,0);
  await h.elements.get('match-comparison-form').onsubmit({preventDefault(){}});
  assert.equal(h.decisionRequests.length,1);assert.equal(h.decisionRequests[0].company,'测试公司');
  assert.equal(h.decisionRequests[0].scope,'ALL');assert.equal(h.decisionRequests[0].api_key,undefined);
  assert.equal(JSON.stringify(h.decisionRequests).includes('synthetic-test-key'),false);
  assert.deepEqual(h.requests,[]);assert.deepEqual(h.exports,[]);
  assert.ok(h.html().includes('暂不能可靠推荐'));
});

test('a delayed comparison cannot reopen a canceled dialog or replace a newly chosen scope',async()=>{
  let resolve;const h=harness({compare:()=>new Promise(r=>{resolve=r;})});await h.start();h.elements.get('match-open-comparison').onclick();
  const request=h.elements.get('match-comparison-form').onsubmit({preventDefault(){}});
  h.elements.get('match-comparison-dialog').close();
  resolve({total:99,recommendation:'READY',reasons:['old-response'],jobs:[]});await request;
  assert.ok(!h.html().includes('old-response'));assert.equal(h.elements.get('match-comparison-dialog').open,false);
});

test('company comparison rejects excessive and empty selected scopes without truncation or a request',async()=>{
  const h=harness();h.snapshot.jobs=Array.from({length:201},(_,i)=>({job:{id:String(i),company:'测试公司',title:'服务端开发',locations:[]},state:'BASIC',local:{tier:'HIGH'},preliminary_score:50}));
  await h.start();h.elements.get('match-open-comparison').onclick();await h.elements.get('match-comparison-form').onsubmit({preventDefault(){}});
  assert.equal(h.decisionRequests.length,0);assert.ok(h.html().includes('对比最多 200'));
  h.elements.get('match-comparison-scope').onchange({target:{value:'SELECTED'}});
  await h.elements.get('match-comparison-form').onsubmit({preventDefault(){}});
  assert.equal(h.decisionRequests.length,0);assert.ok(h.html().includes('没有岗位'));
});
