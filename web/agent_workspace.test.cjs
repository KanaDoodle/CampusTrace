const {test}=require('node:test'),assert=require('node:assert/strict');
const workspace=require('./agent_workspace.js'),{esc}=require('./ui.js');
test('memory controls expose explicit saving, editing, forgetting and opt-in continuation',()=>{
 const html=workspace.markup();assert.match(html,/保存这条记忆/);assert.match(html,/确认过的偏好/);assert.match(html,/不会变成技术能力证明/);assert.match(html,/你选中的讨论摘要/);assert.match(html,/30 天/);
});
