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
 const p={...plan(),evidence_reviews:2};assert.ok(Decision.renderPreparation(p,{esc,D}).includes('2 项错误能力引用已撤销'));
 const html=Decision.renderCompany({scope:'ALL',total:1,analyzed:1,pending:0,stale:0,recommendation:'NONE',reasons:[],jobs:[{job:{id:'1',title:'测试岗位'},state:'ANALYZED',evidence_reviews:2,score:null,coverage:50,strengths:[],gaps:[],sections:[]}]},{esc,D});
 assert.ok(html.includes('2 项错误引用已撤销'));assert.ok(html.includes('暂无可靠评分'));
});
