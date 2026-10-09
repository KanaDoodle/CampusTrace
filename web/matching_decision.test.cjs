const {test}=require('node:test');
const assert=require('node:assert/strict');
const Decision=require('./matching_decision.js');
const D=require('./display.js');
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const task={id:'task',category:'REQUIRED',kind:'EVIDENCE',title:'<script>技术</script>',priority:1,result:'NO_EVIDENCE',action:'暂无依据不等于不会。',requirements:[{id:'r1',text:'Go',excerpt:'<img onerror=alert(1)>'}],evidence:[]};
const plan=()=>({version:'decision-v1',job:{id:'j'},state:'ANALYZED',input_key:'input',analyzed_at:'2026-09-28T05:00:00Z',score:null,coverage:20,tasks:[task],history_topics:[{topic:'历史并发问题',occurrence_count:2}]});

test('checklist progress stays in the browser and is invalidated by account, job, model input, version or new analysis',()=>{
 const values=new Map();global.localStorage={getItem:k=>values.get(k),setItem:(k,v)=>values.set(k,v)};
 const p=plan();assert.equal(Decision.saveProgress('alice',p,new Set(['task','forged'])),true);
 assert.deepEqual([...Decision.readProgress('alice',p)],['task']);
 assert.equal(Decision.readProgress('bob',p).size,0);
 for(const patch of [{job:{id:'other'}},{input_key:'changed'},{version:'v2'},{analyzed_at:'2026-09-28T06:00:00Z'},{state:'STALE'}])assert.equal(Decision.readProgress('alice',{...p,...patch}).size,0);
 assert.ok(!JSON.stringify([...values]).includes('历史并发问题'));
 global.localStorage={getItem:()=>{throw Error('blocked')},setItem:()=>{throw Error('blocked')}};
 assert.equal(Decision.readProgress('alice',p).size,0);assert.equal(Decision.saveProgress('alice',p,new Set()),false);
});
test('preparation displays grounded requirements, distinguishes absent evidence and separates historical review',()=>{
 const html=Decision.renderPreparation(plan(),{esc,D});
 assert.ok(html.includes('暂无依据不等于不会'));
 assert.ok(html.includes('不等同于本岗位'));
 assert.ok(html.includes('&lt;script&gt;'));assert.ok(!html.includes('<img onerror'));
 assert.ok(html.includes('暂无可靠评分'));
 const stale=Decision.renderPreparation({...plan(),state:'STALE',input_key:'',tasks:[],history_topics:[]},{esc,D});
 assert.ok(!stale.includes('data-prep-task='));assert.ok(stale.includes('data-prep-analyze'));
});
test('comparison preserves multiple recommendations, scopes partial results and suppresses stale scores',()=>{
 const report={scope:'SELECTED',total:3,analyzed:2,pending:0,stale:1,recommendation:'TIED',generated_at:'2026-09-28T05:00:00Z',reasons:['仅本次范围'],jobs:[{job:{id:'1',title:'<script>岗位</script>',locations:['上海']},state:'ANALYZED',recommended:true,score:75,coverage:100,eligibility:'UNKNOWN',strengths:[task],gaps:[],sections:[]},{job:{id:'2',title:'并列岗位'},state:'ANALYZED',recommended:true,score:75,coverage:100,eligibility:'UNKNOWN',strengths:[],gaps:[],sections:[]},{job:{id:'3',title:'过期岗位'},state:'STALE',score:null,strengths:[],gaps:[],sections:[]}]};
 const html=Decision.renderCompany(report,{esc,D});assert.equal((html.match(/优先候选/g)||[]).length,2);assert.ok(html.includes('本次已选岗位'));assert.ok(html.includes('分析待更新'));assert.ok(!html.includes('<script>'));assert.ok(html.includes('不能据此认定是全公司'));
});


test('preparation and company comparison surface withdrawn evidence reviews',()=>{
 const p={...plan(),evidence_reviews:2};assert.ok(Decision.renderPreparation(p,{esc,D}).includes('2 项结论已本地复核'));
 const html=Decision.renderCompany({scope:'ALL',total:1,analyzed:1,pending:0,stale:0,recommendation:'NONE',reasons:[],jobs:[{job:{id:'1',title:'测试岗位'},state:'ANALYZED',evidence_reviews:2,score:null,coverage:50,strengths:[],gaps:[],sections:[]}]},{esc,D});
 assert.ok(html.includes('2 项结论已本地复核'));assert.ok(html.includes('暂无可靠评分'));
});


test('company comparison displays low-coverage priority and uncertainty without exposing stale priority',()=>{
 const row={job:{id:'j',title:'后端开发'},state:'ANALYZED',priority:{score:60,lower:20,upper:100},score:null,coverage:20,strengths:[],gaps:[],sections:[]};
 const report={scope:'ALL',total:1,analyzed:1,pending:0,stale:0,recommendation:'READY',reasons:[],jobs:[row]};
 const html=Decision.renderCompany(report,{esc,D});
 assert.ok(html.includes('投递优先度'));assert.ok(html.includes('60.0'));assert.ok(html.includes('参考区间 20.0–100.0'));assert.ok(html.includes('不必等资料全部补齐'));
 const stale=Decision.renderCompany({...report,jobs:[{...row,state:'STALE',score:99,priority:{score:99,lower:99,upper:99}}]},{esc,D});
 assert.ok(!stale.includes('99.0'));assert.ok(stale.includes('待分析或更新'));
});

test('whole assessments and company ranking escape content and never expose item-count scores',()=>{
 const finding={point:'工程能力<script>',explanation:'通过事务处理失败',job_excerpt:'Go开发服务',evidence:[{id:'p',excerpt:'事务与重试'}]};
 const whole={version:'holistic-v1',fit:'RELATED',summary:'可考虑投递',core_work:'服务开发',strengths:[finding],gaps:[],blockers:[],questions:['确认规模'],next_steps:['讲解恢复机制'],ignored_factors:['热爱技术']};
 const html=Decision.renderHolistic(whole,{esc});assert.match(html,/可考虑投递/);assert.match(html,/工程能力&lt;script&gt;/);assert.match(html,/事务与重试/);assert.ok(!html.includes('<script>'));assert.ok(!html.includes('100%'));
 const report={company:'公司',total:2,holistic_job_ids:['a','b'],holistic:{summary:'首选后端，基础平台备选',analyzed_at:'now',choices:[{job_id:'b',rank:2,reason:'基础可迁移',advantage:'系统知识',tradeoff:'领域需准备',job_excerpt:'原文',evidence:[]},{job_id:'a',rank:1,reason:'核心工作更相近',advantage:'服务实践',tradeoff:'规模需核实',job_excerpt:'原文',evidence:[]}]},jobs:[{job:{id:'a',title:'后端'},state:'ANALYZED'},{job:{id:'b',title:'基础平台'},state:'BASIC'}]};
 const comparison=Decision.renderCompany(report,{esc,D});assert.match(comparison,/首选/);assert.match(comparison,/备选/);assert.ok(comparison.indexOf('核心工作更相近')<comparison.indexOf('基础可迁移'));assert.ok(!comparison.includes('投递优先度'));assert.match(comparison,/未覆盖官网全部岗位/);
 const prep=Decision.renderPreparation({...plan(),holistic:whole,history_topics:[{topic:'复盘知识点'}]},{esc,D},new Set(['task']));assert.match(prep,/data-prep-count/);assert.match(prep,/prep-task is-done/);assert.match(prep,/复盘知识点/);assert.ok(!prep.includes('暂无可靠评分'));
});
