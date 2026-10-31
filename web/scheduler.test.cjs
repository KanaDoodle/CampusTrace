const {test}=require('node:test');const assert=require('node:assert/strict');const T=require('./matching_tasks.js');
const deps={esc:String,D:{date:v=>v,errorCode:v=>v,matchingDiagnostic:()=>''}};
test('matching status shows distinct waiting and execution deadlines and preserves reviewed recovery',()=>{
 const run={id:'task',state:'RUNNING',execution_state:'QUEUED',queue_until:'wait-deadline',items:[{job_id:'job',state:'QUEUED'}]};
 let html=T.render(run,[run],deps);assert.match(html,/等待执行名额.*wait-deadline/);assert.doesNotMatch(html,/执行截止/);
 html=T.render({...run,execution_state:'EXECUTING',execution_until:'execution-deadline'},[run],deps);assert.match(html,/执行截止 execution-deadline/);
 html=T.render({...run,state:'WAITING_AUTH',execution_state:'MATCH_QUEUE_TIMEOUT'},[run],deps);assert.match(html,/MATCH_QUEUE_TIMEOUT/);assert.match(html,/核对并继续/);
});
