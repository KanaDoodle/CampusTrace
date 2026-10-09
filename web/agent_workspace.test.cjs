const {test}=require('node:test'),assert=require('node:assert/strict');
const workspace=require('./agent_workspace.js'),practice=require('./practice.js'),{esc}=require('./ui.js');
test('memory controls expose explicit saving, editing, forgetting and opt-in continuation',()=>{
 const html=workspace.markup();assert.match(html,/保存这条记忆/);assert.match(html,/确认过的偏好/);assert.match(html,/不会变成技术能力证明/);assert.match(html,/你选中的讨论摘要/);assert.match(html,/30 天/);
});
test('practice output remains escaped and distinguishes failing, interrupted and truncated runs',()=>{
 for(const state of ['PASSED','FAILED','INTERRUPTED','TIMEOUT','OOM']){const html=practice.render({state,output:'<script>alert(1)</script>',created_at:'2026-10-09T00:00:00Z',truncated:true},esc);assert.ok(html.includes(practice.states[state]));assert.match(html,/&lt;script&gt;/);assert.doesNotMatch(html,/<script>/);assert.match(html,/输出已截短/);}
});
test('practice starter includes executable tests and no invented performance result',()=>{
 assert.match(practice.example.code,/package exercise/);assert.match(practice.example.tests,/func TestDeduplicate/);assert.doesNotMatch(practice.example.code,/QPS|万|生产/);
});
