'use strict';
const test=require('node:test'),assert=require('node:assert/strict');
global.CampusApplications=require('./applications.js');global.CampusCampaigns=require('./campaigns.js');global.CampusDecision=require('./matching_decision.js');
const C=require('./company_decision.js'),esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])),D={date:v=>v,label:(_g,v)=>v};
function fixture(){return {comparison:{company:'合成公司',scope:'SELECTED',total:3,pending:1,stale:1,holistic_job_ids:['a','b'],holistic:{summary:'先比较核心工作',analyzed_at:'2026-10-09',choices:[{job_id:'b',rank:2,reason:'备选理由',advantage:'事务实践',tradeoff:'领域待核对',job_excerpt:'Go',evidence:[]},{job_id:'a',rank:1,reason:'首选理由',advantage:'核心相近',tradeoff:'规模待核对',job_excerpt:'Go开发<script>',evidence:[{excerpt:'项目实际实现'}]}]},jobs:[{job:{id:'b',title:'备选',locations:['上海']},state:'STALE'},{job:{id:'a',title:'首选',locations:['上海']},state:'ANALYZED'},{job:{id:'c',title:'待分析',locations:[]},state:'BASIC'}]},workflow:{as_of:'2026-10-09',jobs:[{job_id:'a',current_status:'OPEN',campaign_id:'rule',official_url:'https://example.com/job/a'},{job_id:'b',current_status:'CLOSED'},{job_id:'c',current_status:'UNKNOWN'}],campaigns:[{id:'rule',name:'2027批次',limit:1,planned:1,submitted:0,remaining:0,job_ids:['a','outside']}],applications:[{id:'app',job_id:'outside',current_state:'PLANNED',job:{title:'范围外的已选岗位'}}]}};}
test('ranks and action context are integrated without ranking pending jobs',()=>{const html=C.renderWorkspace(fixture(),{esc,D});assert.ok(html.indexOf('首选理由')<html.indexOf('备选理由'));assert.match(html,/未参与当前排序/);assert.match(html,/范围外的已选岗位/);assert.match(html,/本批名额已占用/);assert.match(html,/已截止或关闭/);assert.match(html,/未覆盖官网全部岗位/);assert.match(html,/缩小比较范围不会释放名额/);assert.ok(!html.includes('<script>'));assert.match(html,/&lt;script&gt;/);});
test('planning uses the job membership rather than a company-wide minimum quota',()=>{const rules=[{id:'full',remaining:0},{id:'free',remaining:1}];assert.equal(C.planningState({campaign_id:'free',current_status:'OPEN'},rules).allowed,true);assert.equal(C.planningState({campaign_id:'full',current_status:'OPEN'},rules).allowed,false);assert.equal(C.planningState({current_status:'UNKNOWN'},rules).allowed,true);assert.equal(C.planningState(null,rules).allowed,false);assert.equal(C.planningState({application:{id:'app'},current_status:'CLOSED'},rules).label,'查看投递进展');});
test('no company comparison produces no guessed numerical ranking',()=>{const data=fixture();delete data.comparison.holistic;const html=C.renderWorkspace(data,{esc,D});assert.match(html,/整体比较后才给出同公司顺序/);assert.ok(!html.includes('本次首选'));assert.ok(!html.includes('投递优先度'));assert.match(html,/准备清单/);});
test('plan confirmation is explicit and official links reject executable URLs',()=>{const data=fixture();data.workflow.jobs[0].campaign_id='';data.workflow.jobs[0].official_url='javascript:alert(1)';const html=C.renderWorkspace(data,{esc,D},'a');assert.match(html,/确认加入计划/);assert.match(html,/这里只登记计划/);assert.ok(!html.includes('href="javascript:'));assert.match(html,/未登记此岗位的限投规则/);});

test('source date-only deadline is displayed as end of that day',()=>{const data=fixture();data.workflow.jobs[0].deadline='2026-10-13T00:00:00+08:00';data.workflow.jobs[0].deadline_date='2026-10-12';const html=C.renderWorkspace(data,{esc,D});assert.match(html,/截止 2026-10-12（当天结束）/);assert.ok(!html.includes('截止 2026-10-13'));});

test('candidate ordering keeps current holistic tiers separate from historical numeric scores',()=>{
 const row=(id,state,fit,score)=>({job:{id,title:id,locations:['上海']},state,fit,score});
 const rows=[row('old','ANALYZED','',96),row('related','ANALYZED','RELATED',null),row('strong','ANALYZED','STRONG',null),row('stale','STALE','STRONG',100),row('basic','BASIC','',null)];
 assert.deepEqual(C.candidateRows(rows).map(r=>r.job.id),['strong','related','old','basic','stale']);
 assert.deepEqual(C.candidateRows(rows,{state:'ANALYZED',fit:'RELEVANT'}).map(r=>r.job.id),['strong','related']);
 assert.equal(C.candidateScore(rows[3],'technical'),null);
 assert.deepEqual(C.candidateRows(rows,{sort:'technical'}).map(r=>r.job.id),['old','basic','related','stale','strong']);
});

test('picker state is scoped to account and company and contains only bounded navigation preferences',()=>{
 C.storeState('alice','A',{selected:['a','a'],scope:['b'],selectedScope:true,search:'后端',state:'ANALYZED',sort:'deep',page:2,api_key:'should-not-save',document:'private'});
 assert.deepEqual(C.readState('alice','A').selected,['a']);assert.equal(C.readState('alice','A').page,2);assert.deepEqual(C.readState('bob','A').selected,[]);assert.deepEqual(C.readState('alice','B').selected,[]);
 assert.ok(!JSON.stringify(C.readState('alice','A')).includes('private'));assert.ok(!JSON.stringify(C.readState('alice','A')).includes('should-not-save'));
});

test('quick selection preferences accept only the supported batch sizes',()=>{
 C.storeState('quick-user','A',{quickCount:16});
 assert.equal(C.readState('quick-user','A').quickCount,16);
 assert.equal(C.readState('quick-user','B').quickCount,4);
 C.storeState('quick-user','A',{quickCount:1000});
 assert.equal(C.readState('quick-user','A').quickCount,4);
});
