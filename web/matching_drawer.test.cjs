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
test('detail primary actions keep the official link and plan visible while secondary actions are disclosed',()=>{
 const html=M.drawerActions({state:'ANALYZED'},{application:null,applicationReady:true,loading:false,officialURL:A.officialLink('https://careers.example.invalid/role')},helpers);
 const primary=html.split('<div class="drawer-actions">')[1].split('</div>')[0];
 assert.match(primary,/data-detail-plan/);assert.match(primary,/data-detail-compare/);assert.match(primary,/查看官网岗位/);assert.match(primary,/data-detail-analyze disabled hidden/);
 assert.doesNotMatch(primary,/data-detail-pref/);assert.match(html,/<details class="drawer-more-actions">/);
 assert.match(M.detailNavigationHTML([{job:{id:'a'}}],'a',{expanded:true}),/aria-expanded="true">收起阅读/);
});

test('detail navigation follows the filtered order and never wraps to an unrelated or absent job',()=>{
 const visible=['third','first','last'].map(id=>({job:{id}}));
 assert.deepEqual(M.detailPosition(visible,'first'),{index:1,total:3,previous:'third',next:'last'});
 assert.equal(M.detailPosition(visible,'third').previous,'');assert.equal(M.detailPosition(visible,'last').next,'');
 assert.deepEqual(M.detailPosition(visible,'hidden'),{index:-1,total:3,previous:'',next:''});
 assert.match(M.detailNavigationHTML(visible,'first'),/当前结果 2 \/ 3/);
 const absent=M.detailNavigationHTML(visible,'hidden');assert.match(absent,/不在当前筛选结果中/);assert.equal((absent.match(/data-detail-step="-?1" disabled/g)||[]).length,2);
 assert.match(absent,/返回列表/);assert.deepEqual(visible.map(j=>j.job.id),['third','first','last']);
});
