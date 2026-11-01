const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs');
const A=require('./preparation_agent.js');const esc=s=>String(s).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;');
function task(){return {id:'a'.repeat(32),state:'COMPLETED',can_resume:false,preparation_agent:{revision:1,input:{minutes:60,days:3},model_calls:2,search_calls:1,model_label:'controlled-response',evidence_kind:'controlled-model-response',missing:['本申请无历史复盘'],sources:[{id:'source',kind:'JD',title:'冻结岗位',text:'Go <script>owned()</script>'}],result:{summary:'准备并发',items:[{id:'go',basis:'JD',title:'Go 并发',minutes:60,day:1,action:'实现取消 <img onerror=x>',deliverable:'通过竞态测试',references:[{source_id:'source',quote:'Go'}]}]},trace:[{kind:'tool',name:'search_preparation_knowledge',status:'EMPTY'}]}}}
test('plan separates evidence kinds, budget, actions and literal references',()=>{const v=task(),html=A.render(v,esc);assert.equal(A.total(v.preparation_agent.result),60);for(const text of ['60 / 60 分钟','岗位要求','完成标准','冻结岗位','本申请无历史复盘','核对并保存计划','已经掌握，不再安排'])assert.ok(html.includes(text),text);assert.ok(!html.includes('<script>owned'));assert.ok(!html.includes('<img onerror'));});
test('unknown model attempts require explicit retry acknowledgement',()=>{const v=task();v.state='PAUSED';v.can_resume=true;v.preparation_agent.provider_state='UNKNOWN';delete v.preparation_agent.result;const html=A.render(v,esc);assert.ok(html.includes('prep-retry-unknown'));assert.ok(html.includes('可能已处理或计费'));assert.ok(!html.includes('data-prep-save'));});
test('saved and stale records do not expose a save action',()=>{const v=task();v.preparation_agent.saved_at='2026-11-01';assert.ok(!A.render(v,esc).includes('data-prep-save'));delete v.preparation_agent.saved_at;v.state='STALE';assert.ok(!A.render(v,esc).includes('data-prep-save'));});
test('clarification and feedback preserve a separate review before generation',()=>{const v=task();v.state='PAUSED';v.preparation_agent.question='还有多少分钟？';const html=A.render(v,esc);assert.ok(html.includes('补充信息后继续准备'));assert.ok(html.includes('核对下一版输入'));const source=fs.readFileSync(__dirname+'/preparation_agent.js','utf8');assert.ok(source.includes("'/api/agent/preparation/preview'"));assert.ok(source.includes("'/agent/actions/'+pending.action_id+'/confirm'"));assert.ok(source.includes('tick!==revision'));});
test('application dossier and question workspace both expose the plan entry',()=>{const dossier=fs.readFileSync(__dirname+'/journey.js','utf8'),harness=fs.readFileSync(__dirname+'/agent_harness.js','utf8');assert.ok(dossier.includes('dossier-agent-plan'));assert.ok(harness.includes('agent-preparation-open'));assert.ok(harness.includes('!v.preparation_agent'));});

test('progress separates business steps and keeps the executor in diagnostics',()=>{
 const v={state:'RUNNING',plan:[{name:'search_preparation_knowledge'}],next_step:0,preparation_agent:{provider_state:'READY'}};
 assert.match(A.progress(v),/补查/);
 v.plan[0].name='finish_preparation_plan';assert.match(A.progress(v),/核对时间/);
 v.preparation_agent.question='请补充准备时间';assert.match(A.progress(v),/等待补充/);
});

test('clarification explicitly collects both minutes and days in reviewed feedback',()=>{
 const v=task();v.state='PAUSED';v.preparation_agent.input.days=0;v.preparation_agent.question='几天内准备？';
 const html=A.render(v,esc);assert.ok(html.includes('name="days"'));assert.ok(html.includes('几天内完成'));
 const source=fs.readFileSync(__dirname+'/preparation_agent.js','utf8');assert.ok(source.includes('days:Number(feedback.elements.days.value)'));
});

test('quality plans explain evidence use and bounded work without asserting semantic verification',()=>{
 const v=task();v.preparation_agent.result.items[0].workload={mode:'ORAL',units:1,scope:'一个问题的口述与自查'};
 v.preparation_agent.result.items[0].references[0].supports='岗位要求并发，对应取消传播复习';
 v.preparation_agent.retrievals=[{gap:'缺少核对点',expected_use:'用于口述自查',new_source_ids:['k'],cited_source_ids:[]}];
 const html=A.render(v,esc);assert.ok(html.includes('练习规模'));assert.ok(html.includes('用于说明'));assert.ok(html.includes('最终计划引用 0 份'));assert.ok(!html.includes('语义验证通过'));
});
