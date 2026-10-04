const {test}=require('node:test'),assert=require('node:assert/strict'),M=require('./matching.js'),U=require('./ui.js'),D=require('./display.js'),A=require('./applications.js');
const helpers={esc:U.esc,D,U};
test('job actions reflect saved preferences and applied stage without offering a second plan',()=>{
  const html=M.drawerActions({state:'ANALYZED',disposition:'SAVED'},{application:{current_state:'INTERVIEW'},loading:false,applicationReady:true,officialURL:A.officialLink('https://careers.example.invalid/role')},helpers);
  assert.match(html,/查看投递进展/);assert.match(html,/面试/);assert.ok(!html.includes('加入投递计划'));assert.match(html,/取消稍后看/);assert.match(html,/data-detail-pref="SAVED" aria-pressed="true"/);assert.match(html,/rel="noopener noreferrer"/);assert.match(html,/data-detail-analyze disabled/);
});
test('unknown application status can be retried and ignored jobs cannot trigger deep analysis',()=>{
  const html=M.drawerActions({state:'BASIC',disposition:'IGNORED',excluded_reason:'已忽略'},{application:null,loading:false,applicationReady:false,officialURL:A.officialLink('javascript:alert(1)')},helpers);
  assert.match(html,/重试读取投递状态/);assert.match(html,/取消忽略/);assert.ok(!html.includes('加入投递计划'));assert.ok(!html.includes('href='));assert.match(html,/data-detail-analyze disabled/);
  const loading=M.drawerActions({state:'BASIC'},{loading:true},helpers);assert.match(loading,/data-detail-plan disabled/);
});
