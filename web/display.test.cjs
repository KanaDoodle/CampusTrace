const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const D=require('./display.js');
test('岗位状态文案符合约定，未知结果按业务维度区分',()=>{
  assert.deepEqual(Object.values(D.enums.job),['可投递','已关闭','待核验','暂无法确认']);
  assert.equal(D.label('eligibility','UNKNOWN'),'资格暂无法判断');
  assert.equal(D.label('fit','UNKNOWN'),'技术方向暂无法判断');
  assert.equal(D.label('application','OFFER'),'已获录用意向');
  assert.equal(D.label('fact','PLANNED'),'计划实现');
});
test('枚举和字段都有中文标签，意外取值不泄露原始内部标记',()=>{
  for(const group of Object.values(D.enums))for(const value of Object.values(group))assert.match(value,/\p{Script=Han}/u);
  for(const value of Object.values(D.fields))assert.match(value,/\p{Script=Han}/u);
  assert.equal(D.label('job','FUTURE_STATE'),'暂未说明');
  assert.equal(D.label('job','constructor'),'暂未说明');
});
test('时间按北京时间跨日显示，日期型截止日不发生时区偏移',()=>{
  assert.equal(D.date('2026-09-09T18:00:00Z'),'2026年9月10日 02:00（北京时间）');
  assert.equal(D.date('2026-09-09T23:00:00-07:00'),'2026年9月10日 14:00（北京时间）');
  assert.equal(D.date('2026-09-10',true),'2026年9月10日');
  assert.equal(D.date('0001-01-01T00:00:00Z'),'暂未记录');
  assert.equal(D.date('invalid'),'时间待确认');
  assert.equal(D.shanghaiISO('2026-10-01T09:30'),'2026-10-01T01:30:00.000Z');
});
test('表单中文展示能往返为原有英文规范值，不改 API 枚举',()=>{
  const raw=['Shanghai','Hangzhou','Computer Science'];
  assert.deepEqual(D.parseList(D.inputList(raw)),raw);
  assert.equal(D.requirement('REQUIRED:go|java+redis','TECH_STACK'),'必须掌握：（go 或 java） 和 redis');
  assert.equal(D.scalar('candidate_value','go|redis|mysql',{rule:'TECH_STACK'}),'go、redis、mysql');
  assert.equal(D.scalar('candidate_value','0',{rule:'EXPERIENCE_REQUIREMENT'}),'0 个月相关经验');
  assert.equal(D.scalar('requirement','0',{rule:'EXPERIENCE_REQUIREMENT'}),'不限相关经验');
  assert.equal(D.scalar('status','UNKNOWN',{results:[]}),'资格暂无法判断');
});
test('接口错误和新增规则说明使用中文兜底',()=>{
  for(const code of [400,401,403,404,409,429,500])assert.match(D.error(code),/\p{Script=Han}/u);
  assert.match(D.error(401,'/auth/login'),/邮箱或密码/);
  assert.match(D.error(409,'/api/applications/transition'),/刷新/);
  assert.doesNotMatch(D.reason('New English internal explanation'),/English/);
});
// Run the actual render helpers without a browser so nested presentation and
// source-text escaping can be regression-tested without introducing a UI framework.
const context={CampusDisplay:D,sessionStorage:{getItem:()=>''},document:{querySelector:()=>null},globalThis:{},Intl,Date,Number,Object,Array,JSON,String,Error};
vm.createContext(context);
const code=fs.readFileSync(__dirname+'/app.js','utf8');
vm.runInContext(code.slice(0,code.indexOf("formAction('#login'")),context);
const render=(value,key='')=>{context.fixture=value;context.fixtureKey=key;return vm.runInContext('translated(fixture,fixtureKey)',context);};
test('嵌套得分、投递条件和操作预览不显示英文 JSON 键或枚举',()=>{
  const html=render({eligibility:{status:'UNKNOWN',results:[]},ranking:{breakdown:{status:30,city:10}},args:{state:'APPLIED'},action_type:'transition_application'});
  assert.match(html,/资格暂无法判断/);assert.match(html,/岗位可投递情况/);assert.match(html,/30.0 分/);assert.match(html,/已投递/);
  assert.doesNotMatch(html,/UNKNOWN|APPLIED|transition_application|breakdown|"status"/);
});
test('证据摘录保留来源内容并转义，不能当作界面代码执行',()=>{
  const html=render('<img src=x onerror=alert(1)> CLOSED','excerpt');
  assert.match(html,/来源原文，未作翻译/);assert.match(html,/CLOSED/);
  assert.match(html,/&lt;img/);assert.doesNotMatch(html,/<img/);
});
test('个人资料与复盘不再暴露 JSON 编辑器，所有静态表单文案为中文',()=>{
  assert.doesNotMatch(code,/name="json"|JSON\.stringify\(p,null/);
  const html=fs.readFileSync(__dirname+'/index.html','utf8');
  assert.match(html,/lang="zh-CN"/);assert.match(html,/岗位/);
  assert.doesNotMatch(html,/>Jobs<|>Agent<|>Applications<|placeholder="Email"|Full Interview Edition/);
});

test('准备材料区分当前与历史，输出上限可识别',()=>{ assert.equal(D.field('current_requirements'),'当前岗位要求'); assert.equal(D.field('historical_requirements'),'历史岗位要求'); assert.match(D.label('terminal','OUTPUT_LIMIT'),/输出上限/); });
