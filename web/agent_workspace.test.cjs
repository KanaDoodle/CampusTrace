const {test}=require('node:test'),assert=require('node:assert/strict');
const workspace=require('./agent_workspace.js'),{esc}=require('./ui.js');
test('memory controls expose explicit saving, editing, forgetting and opt-in continuation',()=>{
 const html=workspace.markup();assert.match(html,/保存这条记忆/);assert.match(html,/已确认的求职偏好/);assert.match(html,/延续你的选择/);assert.match(html,/选中的讨论摘要/);assert.match(html,/30 天/);
});
