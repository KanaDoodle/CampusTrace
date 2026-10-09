const {test}=require('node:test'),assert=require('node:assert/strict');
const harness=require('./agent_harness.js'),{esc}=require('./ui.js');
test('task requests keep a replay key and only the chosen task input',()=>{const v=harness.requestInput({id:'review-plan',input:'topic'},'  Go 并发  ','same-nonce');assert.deepEqual(v,{skill_id:'review-plan',request_key:'same-nonce',topic:'Go 并发'});assert.deepEqual(harness.requestInput({id:'daily-review',input:'none'},'ignored','same-nonce'),{skill_id:'daily-review',request_key:'same-nonce'});});
test('task output is escaped and only resumable states expose continuation',()=>{for(const state of ['PAUSED','PENDING','COMPLETED','CANCELLED','STALE']){const html=harness.renderExecution({id:'safe',state,created_at:'2026-10-09',answer:'<script>evil</script>',next_step:1,plan:[{name:'search_jobs'}],failures:[{next_step:'<img src=x>'}]},esc);assert.doesNotMatch(html,/<script>|<img/);assert.match(html,/&lt;script&gt;/);assert.equal(html.includes('data-execution-resume'),['PAUSED','PENDING'].includes(state));assert.match(html,/data-execution-details/);}});
test('stored MCP credentials accept bounded account-owned connector IDs only',()=>{const id='a'.repeat(32);assert.deepEqual(harness.cleanCredentials({[id]:'token',other:'bad', ['b'.repeat(32)]:'bad\nheader', ['c'.repeat(32)]:'x'.repeat(2049)}),{[id]:'token'});assert.deepEqual(harness.cleanCredentials(['bad']),{});});
test('continuation honors server lease and attempt limit; recovered warnings are folded away',()=>{
 assert.equal(harness.canResume({state:'RUNNING',updated_at:'2000-01-01',can_resume:false}),false);
 assert.equal(harness.canResume({state:'RUNNING',can_resume:true}),true);
 assert.equal(harness.canResume({state:'PAUSED',resumes:5}),false);
 const html=harness.renderExecution({id:'safe',state:'COMPLETED',created_at:'2026-10-09',skill_id:'company-choice',plan:[{name:'compare_company_jobs',arguments:{company:'腾讯<script>'}}],failures:[{next_step:'此前失败'}]},esc);
 assert.doesNotMatch(html,/class="callout"|data-execution-resume|<script>/);assert.match(html,/曾遇到的问题（已恢复）/);assert.match(html,/腾讯&lt;script&gt;/);
});
test('Chinese task input limits use UTF-8 bytes and distinguish identifiers',()=>{
 assert.equal(harness.inputError({input:'company'},'腾讯'),'');assert.ok(harness.inputError({input:'company'},'中'.repeat(67)));
 assert.equal(harness.inputError({input:'topic'},'中'.repeat(166)),'');assert.ok(harness.inputError({input:'topic'},'中'.repeat(167)));
 assert.ok(harness.inputError({input:'job_id'},'bad'));assert.equal(harness.inputError({input:'job_id'},'a'.repeat(32)),'');
});
test('optional panel errors are isolated, surfaced and do not prevent other loads',async()=>{
 const loaded=[],failed=[];const results=await harness.loadPanels([{load:async()=>{throw new Error('offline');},failed:e=>failed.push(e.message)},{load:async()=>{loaded.push('question settings');return 'ready';},failed:()=>assert.fail()}]);
 assert.deepEqual(failed,['offline']);assert.deepEqual(loaded,['question settings']);assert.equal(results[0].status,'rejected');assert.equal(results[1].value,'ready');
});
test('usage distinguishes local queries, unknown provider usage and measured tokens',()=>{
 assert.equal(harness.usageText({provider_calls:0,budget:{prompt_tokens:0,completion_tokens:0}}),'');
 assert.match(harness.usageText({provider_calls:1,budget:{unreported_calls:1}}),/未报告/);
 assert.equal(harness.usageText({provider_calls:1,budget:{prompt_tokens:100,completion_tokens:20}}),'已报告 120 token');
 assert.match(harness.usageText({provider_calls:2,budget:{prompt_tokens:100,completion_tokens:20,unreported_calls:1}}),/部分请求未报告/);
});
