'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const D=require('./display.js'),{esc}=require('./ui.js');
const record=(extra={})=>({id:'application-a',job_id:'job-a',version:1,current_state:'APPLIED',next_states:['OA','INTERVIEW'],resume_version:'后端版',job:{company:'甲公司',title:'Go 后端',locations:['上海']},...extra});

function harness({initial=[record()],request,workflow={}}={}){
 const elements=new Map(),forms=new Map(),notices=[],requests=[],navigations=[],positions=[];let html='';
 function parse(value){
  for(const m of value.matchAll(/<([a-z]+)\b([^>]*\bid="([^"]+)"[^>]*)>/gi))if(!elements.has(m[3]))elements.set(m[3],element(m[3],m[2]));
 }
 function element(id,attributes=''){
  let markup='';const children=new Map();
  return {id,value:attributes.match(/value="([^"]*)"/)?.[1]||'',checked:false,disabled:false,
   get innerHTML(){return markup;},set innerHTML(v){markup=v;parse(v);},querySelectorAll(){return [];},
   querySelector(selector){if(!children.has(selector))children.set(selector,{onclick:null});return children.get(selector);}
  };
 }
 const document={querySelector(selector){if(selector.startsWith('[data-application-id='))return elements.get('article');return elements.get(selector.slice(1));},querySelectorAll(){return [];}};
 const draft={capture(){},restore(){},dirty:()=>false,savepoint:()=>null,saved(){},forget(){}};
 const context={document,CampusUI:{notify:message=>notices.push(message)},CampusNavigation:{readWorkflow:()=>workflow,storeWorkflow:(_u,_s,v)=>positions.push(v),register(){},forms:()=>draft},console};
 vm.createContext(context);vm.runInContext(fs.readFileSync(__dirname+'/applications.js','utf8'),context);
 const set=value=>{html=value;parse(value);elements.set('article',element('article'));return true;};
 let reads=0;
 const api=async(path,method,body)=>{
  requests.push({path,method,body});
  if(path==='/api/profile/resume/capabilities')return {user_id:'fixture-user'};
  if(path==='/api/applications'&&reads++===0)return structuredClone(initial);
  return request(path,method,body);
 };
 return {elements,forms,notices,requests,navigations,positions,article:()=>elements.get('article'),html:()=>html,records:()=>elements.get('application-records').innerHTML,
  start:()=>context.CampusApplications.page(set,'<h1>投递进展</h1>',{api,esc,D,formAction:(selector,action)=>forms.set(selector,action),navigate:(name,query)=>navigations.push({name,query}),scheduleInterview(){},table:()=>'',active:()=>true})};
}

test('refreshing applications updates company choices and clears a company that disappeared',async()=>{
 let next=[record(),record({id:'application-b',job_id:'job-b',job:{company:'乙公司',title:'平台开发',locations:[]}})];
 const h=harness({request:async()=>structuredClone(next)});await h.start();
 h.elements.get('application-company').value='甲公司';h.elements.get('application-company').onchange();
 await h.elements.get('application-refresh').onclick();
 assert.match(h.elements.get('application-company').innerHTML,/乙公司/);assert.equal(h.elements.get('application-company').value,'甲公司');
 next=next.slice(1);await h.elements.get('application-refresh').onclick();
 assert.equal(h.elements.get('application-company').value,'');assert.match(h.records(),/乙公司/);assert.doesNotMatch(h.records(),/没有符合条件/);
});

test('saving application details keeps valid stages when only the following list refresh fails',async()=>{
 let version=1;
 const h=harness({request:async(path,method,body)=>{
  if(method==='PUT'){assert.equal(body.version,version);return record({version:++version,resume_version:body.resume_version,next_states:undefined});}
  throw Error('合成的列表读取失败');
 }});await h.start();
 await h.forms.get('#application-details-application-a')(new Map([['resume_version','新版'],['note','']]));
 assert.match(h.records(),/新版/);assert.match(h.records(),/application-stage-application-a/);assert.doesNotMatch(h.records(),/这条投递已结束/);
 assert.ok(h.notices.some(v=>v.includes('列表刷新失败')));
 await h.forms.get('#application-details-application-a')(new Map([['resume_version','第三版'],['note','']]));
 assert.equal(version,3);assert.match(h.records(),/第三版/);
});

test('a successful stage change never reuses old transitions or claims a nonterminal application ended',async()=>{
 const h=harness({request:async(path,method)=>{
  if(method==='POST')return record({version:2,current_state:'OA',next_states:undefined});
  throw Error('合成的列表读取失败');
 }});await h.start();
 await h.forms.get('#application-stage-application-a')(new Map([['state','OA'],['note','']]));
 assert.match(h.records(),/笔试/);assert.doesNotMatch(h.records(),/application-stage-application-a|这条投递已结束/);
 assert.match(h.records(),/请刷新记录后再更新进展/);
});

test('application details and preparation both carry a return context without clearing stored filters',async()=>{
 const h=harness({workflow:{query:'Go',company:'甲公司',ongoingPage:2,applicationSort:'followup',scroll:480}});await h.start();await h.article().querySelector('[data-application-job]').onclick();await h.article().querySelector('[data-application-preparation]').onclick();
 assert.equal(h.navigations.length,2);assert.equal(h.navigations[0].name,'matching');assert.equal(h.navigations[0].query.returnTo.page,'applications');assert.equal(h.navigations[0].query.jobID,'job-a');assert.equal(h.navigations[1].name,'dossier');assert.equal(h.navigations[1].query.applicationID,'application-a');
 assert.equal(h.positions[1].lastApplication,'application-a');assert.equal(h.positions[1].query,'Go');assert.equal(h.positions[1].company,'甲公司');assert.equal(h.positions[1].applicationSort,'followup');
});
