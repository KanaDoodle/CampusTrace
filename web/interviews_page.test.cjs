'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),D=require('./display.js'),{esc}=require('./ui.js');
const records=n=>Array.from({length:n},(_,i)=>({id:'interview-'+i,application_id:'application-'+i,round:1,scheduled_at:'2099-01-01T08:00:00Z',result:'PENDING',notes:'原备注',job:{id:'job-'+i,company:'合成星海',title:'Go 后端 '+String(i).padStart(2,'0'),locations:['上海']}}));
const decode=v=>v.replaceAll('&quot;','"').replaceAll('&#39;',"'").replaceAll('&lt;','<').replaceAll('&gt;','>').replaceAll('&amp;','&');
function harness({rows=records(30),request,stored=new Map(),user='alice'}={}){
 const elements=new Map(),forms=new Map(),calls=[],navigations=[],notices=[];let articles=[],pagers=[],live=true,scroll=0,shells=0,paints=0;
 const document={activeElement:null,querySelector:s=>elements.get(s.slice(1)),querySelectorAll:s=>s==='#interview-records form'?articles.map(a=>a.form):[]};
 function node(id=''){
  let html='';return {id,value:'',disabled:false,textContent:'',open:false,dataset:{},classList:{add(){}},get innerHTML(){return html;},set innerHTML(v){html=v;parseIDs(v);if(id==='interview-records'){paints++;parseRecords(v);}},focus(){document.activeElement=this;},scrollIntoView(){this.scrolled=true;},querySelectorAll:()=>[]};
 }
 function parseIDs(value){for(const m of value.matchAll(/\bid="([^"]+)"[^>]*>/g))if(!elements.has(m[1]))elements.set(m[1],node(m[1]));}
 function parseRecords(value){
  articles=[];pagers=[];
  for(const m of value.matchAll(/<article[^>]*data-interview-id="([^"]+)"[^>]*>([\s\S]*?)<\/article>/g)){
   const id=decode(m[1]),body=m[2],article=node(),children=new Map();article.dataset.interviewId=id;
   for(const attr of ['application','preparation','review'])if(body.includes('data-interview-'+attr+'='))children.set('[data-interview-'+attr+']',node());
   article.querySelector=s=>children.get(s);
   article.editors=[...body.matchAll(/data-interview-editor="([^"]+)"/g)].map(m=>Object.assign(node(),{dataset:{interviewEditor:decode(m[1])}}));
   const result=Object.assign(node('interview-result-value-'+id),{name:'result',type:'select-one',value:body.match(/<option value="([^"]+)" selected/)?.[1]||'PENDING'});
   const notes=Object.assign(node('interview-notes-'+id),{name:'notes',type:'textarea',value:decode(body.match(/<textarea[^>]*>([\s\S]*?)<\/textarea>/)?.[1]||'')});
   const form=Object.assign(node('interview-result-'+id),{elements:[result,notes],button:node()});article.form=form;
   for(const el of [result,notes,form])elements.set(el.id,el);articles.push(article);
  }
  for(const m of value.matchAll(/data-interview-page="([^"]+)"([^>]*)>/g))pagers.push(Object.assign(node(),{dataset:{interviewPage:m[1]},disabled:/disabled/.test(m[2])}));
  const host=elements.get('interview-records');host.querySelectorAll=s=>s==='[data-interview-id]'?articles:s==='[data-interview-editor]'?articles.flatMap(a=>a.editors):s==='[data-interview-page]'?pagers:s==='form button'?articles.map(a=>a.form.button):[];
 }
 const c={document,sessionStorage:{getItem:k=>stored.get(k),setItem:(k,v)=>stored.set(k,v)},get scrollY(){return scroll;},scrollTo:v=>scroll=v.top,requestAnimationFrame:fn=>fn(),CampusUI:{notify:v=>notices.push(v),capture:()=>({id:document.activeElement?.id}),restore:p=>{if(p.id)elements.get(p.id)?.focus();}}};
 vm.createContext(c);for(const name of ['navigation.js','interviews.js'])vm.runInContext(fs.readFileSync(__dirname+'/'+name,'utf8'),c);
 const api=async(path,method='GET',body)=>{calls.push({path,method,body});if(path==='/api/profile/resume/capabilities')return {user_id:user};return request?request(path,method,body):structuredClone(rows);};
 return {elements,calls,forms,navigations,notices,stored,document,articles:()=>articles,pagers:()=>pagers,paints:()=>paints,shells:()=>shells,scroll:y=>scroll=y,getScroll:()=>scroll,html:()=>elements.get('interview-records').innerHTML,feedback:()=>elements.get('interview-feedback').innerHTML,leave:async()=>{await c.CampusNavigation.leave();live=false;},start:options=>c.CampusInterviews.page(value=>{shells++;parseIDs(value);return true;},'<h1>面试记录</h1>',{api,esc,D,formAction:(selector,fn)=>forms.set(selector,fn),navigate:async(name,query)=>{await c.CampusNavigation.leave();navigations.push({name,query});},reviewInterview(){},active:()=>live,...options})};
}
test('interview refresh is read-only, retains page/filter/open editor and unsaved feedback with focus',async()=>{
 const h=harness();await h.start();const search=h.elements.get('interview-search');search.value='Go';search.oninput({});h.pagers()[1].onclick();
 const first=h.articles()[0],id=first.dataset.interviewId;first.editors[0].open=true;const notes=h.elements.get('interview-notes-'+id);notes.value='尚未保存的中文备注';notes.focus();h.scroll(740);
 await h.elements.get('interview-refresh').onclick();assert.match(h.html(),/第 2 \/ 2 页/);assert.equal(search.value,'Go');assert.equal(h.articles()[0].editors[0].open,true);assert.equal(h.elements.get(notes.id).value,'尚未保存的中文备注');assert.equal(h.document.activeElement.id,notes.id);assert.equal(h.getScroll(),740);assert.equal(h.shells(),1);assert.equal(h.calls.filter(v=>v.path==='/api/interviews').length,2);assert.ok(h.calls.every(v=>v.method==='GET'));
});
test('failed and malformed refreshes retain records and have a local read-only retry',async()=>{
 let mode='ready';const h=harness({request:async()=>{if(mode==='fail')throw Error('合成断网');return mode==='malformed'?{}:records(3);}});await h.start();mode='fail';await h.elements.get('interview-refresh').onclick();assert.match(h.feedback(),/合成断网.*保留上次读取/);assert.equal(h.articles().length,3);mode='malformed';await h.elements.get('interview-retry').onclick();assert.match(h.feedback(),/暂时无法读取/);assert.equal(h.articles().length,3);mode='ready';await h.elements.get('interview-retry').onclick();assert.equal(h.feedback(),'');assert.equal(h.articles().length,3);assert.ok(h.calls.every(v=>v.method==='GET'));
});
test('initial failure is retryable without showing a false empty state',async()=>{
 let fail=true;const h=harness({request:async()=>{if(fail)throw Error('合成初次失败');return records(1);}});await h.start();assert.match(h.feedback(),/合成初次失败/);assert.equal(h.html(),'');fail=false;await h.elements.get('interview-retry').onclick();assert.equal(h.articles().length,1);assert.equal(h.shells(),1);
});
test('concurrent refresh clicks deduplicate and finishing a read after leaving cannot repaint',async()=>{
 let resolve,slow=false;const h=harness({request:()=>slow?new Promise(done=>resolve=done):Promise.resolve(records(3))});await h.start();slow=true;const one=h.elements.get('interview-refresh').onclick(),two=h.elements.get('interview-refresh').onclick();assert.equal(h.calls.filter(v=>v.path==='/api/interviews').length,2);const paints=h.paints();await h.leave();resolve(records(1));await Promise.all([one,two]);assert.equal(h.paints(),paints);assert.equal(h.articles().length,3);
});
test('refresh updates company choices and clears only a company absent from the new records',async()=>{
 let next=records(2);const h=harness({request:async()=>structuredClone(next)});await h.start();h.elements.get('interview-company').value='合成星海';h.elements.get('interview-company').onchange();next=[{...records(1)[0],job:{id:'job-other',title:'Go 平台研发',company:'合成橘子'}}];await h.elements.get('interview-refresh').onclick();assert.equal(h.elements.get('interview-company').value,'');assert.match(h.elements.get('interview-company').innerHTML,/合成橘子/);assert.match(h.html(),/Go 平台研发/);
});
test('Chinese search composition and feedback composition defer async redraws',async()=>{
 let resolve,slow=false;const h=harness({request:()=>slow?new Promise(done=>resolve=done):Promise.resolve(records(2))});await h.start();const input=h.elements.get('interview-search'),before=h.paints();input.oncompositionstart();input.value='后端';input.oninput({isComposing:true});assert.equal(h.paints(),before);input.oncompositionend();assert.equal(h.paints(),before+1);
 slow=true;const reading=h.elements.get('interview-refresh').onclick(),host=h.elements.get('interview-records'),atStart=h.paints();host.oncompositionstart();h.elements.get('interview-notes-interview-0').value='新输入的中文';resolve(records(2));await reading;assert.equal(h.paints(),atStart);host.oncompositionend();assert.equal(h.paints(),atStart+1);assert.equal(h.elements.get('interview-notes-interview-0').value,'新输入的中文');
});
test('saving completion excludes refresh while pending and updates locally without a second list request',async()=>{
 let finish;const h=harness({request:async(path,method,body)=>method==='POST'?new Promise(done=>finish=()=>done({...records(1)[0],...body,finished_at:'2026-10-18T08:00:00Z'})):records(1)});await h.start();const saving=h.forms.get('#interview-result-interview-0')(new Map([['result','PASS'],['notes','已收到通过通知']]));assert.equal(h.elements.get('interview-refresh').disabled,true);await h.elements.get('interview-refresh').onclick();assert.equal(h.calls.filter(v=>v.path==='/api/interviews').length,1);finish();await saving;assert.match(h.html(),/已收到通过通知/);assert.match(h.html(),/已记录完成/);assert.equal(h.elements.get('interview-refresh').disabled,false);assert.equal(h.notices.length,1);
});
test('preparation return restores account-specific filters, second page, expanded editor, scroll and origin focus',async()=>{
 const stored=new Map(),h=harness({stored});await h.start();h.elements.get('interview-search').value='Go';h.elements.get('interview-search').oninput({});h.elements.get('interview-view').value='pending';h.elements.get('interview-view').onchange();h.pagers()[1].onclick();h.articles()[0].editors[0].open=true;h.scroll(820);await h.articles()[0].querySelector('[data-interview-preparation]').onclick();assert.equal(h.navigations[0].query.returnTo.page,'interviews');assert.equal(h.navigations[0].query.view,'preparation');
 const returned=harness({stored});await returned.start({resume:true});assert.match(returned.html(),/第 2 \/ 2 页/);assert.equal(returned.elements.get('interview-search').value,'Go');assert.equal(returned.elements.get('interview-view').value,'pending');assert.equal(returned.articles()[0].editors[0].open,true);assert.equal(returned.getScroll(),820);assert.equal(returned.document.activeElement,returned.articles()[0].querySelector('[data-interview-preparation]'));
 const other=harness({stored,user:'bob'});await other.start();assert.equal(other.elements.get('interview-search').value,'');assert.match(other.html(),/第 1 \/ 2 页/);
});

test('a failed completion save preserves feedback and re-enables retry without reporting success',async()=>{
 const h=harness({request:async(_path,method)=>{if(method==='POST')throw Error('合成保存失败');return records(1);}});await h.start();h.articles()[0].editors[0].open=true;h.elements.get('interview-notes-interview-0').value='等待重试的反馈';await assert.rejects(h.forms.get('#interview-result-interview-0')(new Map([['result','PASS'],['notes','等待重试的反馈']])),/合成保存失败/);assert.equal(h.elements.get('interview-notes-interview-0').value,'等待重试的反馈');assert.equal(h.articles()[0].editors[0].open,true);assert.equal(h.elements.get('interview-refresh').disabled,false);assert.equal(h.notices.length,0);assert.match(h.html(),/尚未记录完成/);
});
test('an explicit interview outside the recent list is fetched and located after an initial read retry',async()=>{
 let failed=true;const target={...records(1)[0],id:'older-interview',job:{id:'older-job',company:'合成旧记录',title:'旧轮次'}};const h=harness({request:async path=>{if(path==='/api/interviews'){if(failed)throw Error('合成初次失败');return records(30);}if(path==='/api/interviews/older-interview')return target;throw Error(path);}});await h.start({initialInterview:target.id});failed=false;await h.elements.get('interview-retry').onclick();assert.ok(h.articles().some(a=>a.dataset.interviewId===target.id));assert.match(h.html(),/旧轮次/);assert.equal(h.calls.filter(v=>v.path==='/api/interviews/older-interview').length,1);
});
