'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const J=require('./journey.js'),{esc}=require('./ui.js'),D=require('./display.js');
test('application preparation renders the frozen material, legacy absence and notice provenance',()=>{
 const c={phase:'SUBMITTED',reviewed:true,captured_at:'2026-10-30T10:00:00Z',job:{title:'留档岗位',company:'合成公司'},jd:'当时 JD<script>',candidate_document:'当时个人材料',resume_text:'本次简历',campaigns:[{name:'秋招',limit:2,version:1}],company_reports:[]};
 const v={application:{current_state:'INTERVIEW',job:{title:'后来的岗位'}},context:c,contexts:[c],notices:[{parsed:{subject:'通知',text:'原始通知'},confirmed:{kind:'INTERVIEW',note:'上海'}}],interviews:[]};
 const html=J.dossierHTML(v,{esc,D});assert.match(html,/留档岗位/);assert.doesNotMatch(html,/后来的岗位|<script>/);assert.match(html,/当时 JD&lt;script&gt;/);assert.match(html,/原始通知|本次简历/);assert.match(J.contextHTML(null,{esc,D}),/历史申请没有当时材料快照/);
 assert.equal(J.inputTime('2026-11-02T06:00:00Z'),'2026-11-02T14:00');assert.equal(J.isoTime('2026-11-02T14:00'),'2026-11-02T06:00:00.000Z');
});
function harness(){
 const elements=new Map(),calls=[],navigation=[];let html='',failConfirm=true;
 const document={querySelector:s=>elements.get(s.slice(1)),querySelectorAll:()=>[]};
 function set(value){html=value;elements.clear();for(const m of value.matchAll(/\bid="([^"]+)"/g))elements.set(m[1],{value:'',checked:false,innerHTML:'',textContent:''});return true;}
 const notice={id:'n',version:1,state:'PENDING',parsed:{subject:'合成公司面试改期',text:'原时间与新时间',kind:'RESCHEDULE',warnings:['多个时间需人工核对']}};
 const app={id:'a',version:3,current_state:'INTERVIEW',job:{company:'合成公司',title:'后端'}};
 const api=async(path,method,body)=>{calls.push({path,method,body});if(path==='/api/recruitment-notices'&&method==='POST')return {notice,candidates:[{application:app,reasons:['公司名称']}],ambiguous:true};if(path==='/api/recruitment-notices')return [];if(path==='/api/applications')return [app];if(path==='/api/applications/a/dossier')return {application:app,interviews:[{id:'i',round:1,scheduled_at:'2026-11-02T06:00:00Z'}]};if(path.endsWith('/confirm')){if(failConfirm)throw Error('版本已变化');return {state:'CONFIRMED'};}throw Error(path);};
 const context={document,CampusNavigation:{register(){},confirmDiscard:async()=>true},console};vm.createContext(context);vm.runInContext(fs.readFileSync(__dirname+'/journey.js','utf8'),context);
 return {elements,calls,navigation,html:()=>html,succeed:()=>failConfirm=false,start:()=>context.CampusJourney.page(set,'<h1>招聘通知</h1>',{api,esc,D,navigate:(...args)=>navigation.push(args)})};
}
test('notice UI keeps ambiguity explicit, requires original interview for reschedule and preserves failed confirmation',async()=>{
 const h=harness();await h.start();h.elements.get('notice-content').value='合成通知';await h.elements.get('notice-import').onsubmit({preventDefault(){}});
 assert.match(h.html(),/无法唯一对应申请/);assert.equal(h.elements.get('notice-application').value,'');
 const submit=()=>h.elements.get('notice-confirm-form').onsubmit({preventDefault(){}});
 await submit();assert.equal(h.calls.filter(c=>c.path.endsWith('/confirm')).length,0);
 h.elements.get('notice-application').value='a';await h.elements.get('notice-application').onchange();h.elements.get('notice-reviewed').checked=true;h.elements.get('notice-kind').value='RESCHEDULE';h.elements.get('notice-at').value='2026-11-03T16:00';h.elements.get('notice-round').value='1';
 await submit();assert.match(h.elements.get('journey-status').textContent,/原面试/);
 h.elements.get('notice-interview').value='i';h.elements.get('notice-note').value='选择新时间';await submit();assert.match(h.elements.get('journey-status').textContent,/版本已变化/);assert.equal(h.elements.get('notice-note').value,'选择新时间');assert.equal(h.navigation.length,0);
 const payload=h.calls.find(c=>c.path.endsWith('/confirm')).body;assert.equal(payload.previous_scheduled_at,'2026-11-02T06:00:00Z');assert.equal(payload.scheduled_at,'2026-11-03T08:00:00.000Z');assert.equal(payload.application_version,3);
 h.succeed();await submit();assert.equal(h.navigation[0][0],'dossier');
});

test('ordinary notice confirmation omits absent dates instead of sending null to strict JSON APIs',async()=>{
 const h=harness();await h.start();h.elements.get('notice-content').value='合成通知';await h.elements.get('notice-import').onsubmit({preventDefault(){}});
 h.elements.get('notice-application').value='a';await h.elements.get('notice-application').onchange();h.elements.get('notice-reviewed').checked=true;h.elements.get('notice-kind').value='UNKNOWN';h.elements.get('notice-round').value='1';
 await h.elements.get('notice-confirm-form').onsubmit({preventDefault(){}});const payload=h.calls.find(c=>c.path.endsWith('/confirm')).body;
 assert.ok(!Object.hasOwn(payload,'scheduled_at'));assert.ok(!Object.hasOwn(payload,'previous_scheduled_at'));assert.ok(!Object.values(payload).includes(null));
 h.elements.get('notice-content').value='保留中的编辑';await h.elements.get('notice-refresh').onclick();assert.equal(h.elements.get('notice-content').value,'保留中的编辑');
});
