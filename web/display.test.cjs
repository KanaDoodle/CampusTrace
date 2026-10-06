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
test('核对表区分缺少岗位要求与缺少求职资料',()=>{
  assert.equal(D.scalar('requirement','',{rule:'GRADUATION_REQUIREMENT'}),'尚未提取到明确要求');
  assert.equal(D.scalar('candidate_value','',{rule:'GRADUATION_REQUIREMENT'}),'求职资料中尚未填写');
  assert.equal(D.scalar('candidate_value','2027',{rule:'GRADUATION_REQUIREMENT'}),'2027 届');
  assert.equal(D.scalar('candidate_value','2026-2027',{rule:'GRADUATION_REQUIREMENT'}),'2026—2027 届');
  assert.equal(D.scalar('candidate_value','FULL_TIME|INTERNSHIP',{rule:'JOB_TYPE'}),'全职岗位、实习');
  assert.equal(D.scalar('requirement','',{rule:'EDUCATION_REQUIREMENT',explanation:'Conflicting requirements require verification'}),'要求存在冲突，待核验');
});
test('接口错误和新增规则说明使用中文兜底',()=>{
  for(const code of [400,401,403,404,409,429,500])assert.match(D.error(code),/\p{Script=Han}/u);
  assert.match(D.error(401,'/auth/login'),/邮箱或密码/);
  assert.match(D.error(409,'/api/applications/transition'),/刷新/);
  assert.doesNotMatch(D.reason('New English internal explanation'),/English/);
});
test('服务端错误码优先于状态码兜底，注册重名不再报“必填项”',()=>{
  assert.match(D.errorCode('EMAIL_TAKEN',409,'/auth/register'),/已经注册过/);
  assert.match(D.errorCode('EMAIL_TAKEN',409,'/auth/register'),/登录/);
  // Known codes fall back to the status mapping when absent or unknown.
  assert.equal(D.errorCode(undefined,409,'/auth/register'),D.error(409,'/auth/register'));
  assert.equal(D.errorCode('SOMETHING_ELSE',400,'/api/ingest'),D.error(400,'/api/ingest'));
  assert.match(D.errorCode(undefined,400,'/api/ingest'),/提交未成功/);
});
test('简历模型失败区分超时、密钥、余额与摘录核对',()=>{
  assert.match(D.errorCode('MODEL_TIMEOUT',504),/超时/);
  assert.match(D.errorCode('MODEL_AUTH_FAILED',502),/密钥/);
  assert.match(D.errorCode('MODEL_BALANCE_LOW',502),/余额/);
  assert.match(D.errorCode('RESUME_DRAFT_UNVERIFIABLE',502),/摘录核对/);
});

test('简历诊断区分格式、长度和原文依据，且只显示已知原因及数字位置',()=>{
  assert.match(D.resumeDiagnostic({validation_reason:'EXCERPT_NOT_EXACT',scope:'FACT',project_index:2,item_index:3}),/第 2 个项目的第 3 条事实.*连续原文/);
  assert.match(D.resumeDiagnostic({validation_reason:'EXCERPT_LENGTH',scope:'SUGGESTION',item_index:4}),/资料建议第 4 项.*摘录过长/);
  assert.match(D.resumeDiagnostic({validation_reason:'RESPONSE_JSON'}),/JSON/);
  assert.match(D.resumeDiagnostic({validation_reason:'VALUE_FORMAT'}),/格式/);
  assert.equal(D.resumeDiagnostic({validation_reason:'private resume text'}),'');
  assert.doesNotMatch(D.resumeDiagnostic({validation_reason:'EXCERPT_EMPTY',scope:'FACT',project_index:'private',item_index:3}),/private/);
  assert.doesNotMatch(D.errorCode('RESUME_DRAFT_UNVERIFIABLE',502),/缩短外发文字/);
});
test('招聘源读取失败区分网络、访问限制与接口变化',()=>{
  assert.match(D.errorCode('SOURCE_PREVIEW_NETWORK',502),/网络不通/);
  assert.doesNotMatch(D.errorCode('SOURCE_PREVIEW_NETWORK',502),/接口已变化/);
  assert.match(D.errorCode('SOURCE_PREVIEW_BLOCKED',502),/拒绝/);
  assert.match(D.errorCode('SOURCE_PREVIEW_BUSY',502),/繁忙/);
  assert.match(D.errorCode('SOURCE_PREVIEW_CHANGED',502),/数据格式/);
});
// Run the actual render helpers without a browser so nested presentation and
// source-text escaping can be regression-tested without introducing a UI framework.
const context={CampusDisplay:D,sessionStorage:{getItem:()=>''},document:{querySelector:()=>null},Intl,Date,Number,Object,Array,JSON,String,Error};
context.document.querySelectorAll=()=>[];context.document.addEventListener=()=>{};
vm.createContext(context);vm.runInContext(fs.readFileSync(__dirname+'/ui.js','utf8'),context);
const code=fs.readFileSync(__dirname+'/app.js','utf8');
vm.runInContext(code.slice(0,code.indexOf("formAction('#login'")),context);
const render=(value,key='')=>{context.fixture=value;context.fixtureKey=key;return vm.runInContext('translated(fixture,fixtureKey)',context);};
test('简历 API 错误显示安全的具体原因及请求编号',async()=>{
  context.fetch=async()=>({ok:false,status:502,json:async()=>({code:'RESUME_DRAFT_UNVERIFIABLE',diagnostic:{validation_reason:'EXCERPT_NOT_EXACT',scope:'FACT',project_index:1,item_index:2},request_id:'aabbccddeeff00112233445566778899'}),headers:{get:()=>null}});
  await assert.rejects(vm.runInContext("api('/api/profile/resume/draft','POST',{text:'synthetic reviewed text'})",context),error=>/第 1 个项目的第 2 条事实/.test(error.message)&&/aabbccddeeff00112233445566778899/.test(error.message));
});
test('嵌套得分、投递条件和操作预览不显示英文 JSON 键或枚举',()=>{
  const html=render({eligibility:{status:'UNKNOWN',results:[]},ranking:{breakdown:{status:30,city:10}},args:{state:'APPLIED'},action_type:'transition_application'});
  assert.match(html,/资格暂无法判断/);assert.match(html,/岗位可投递情况/);assert.match(html,/30.0 分/);assert.match(html,/已投递/);
  assert.doesNotMatch(html,/UNKNOWN|APPLIED|transition_application|breakdown|"status"/);
});
test('岗位证据缺失不会把已保存的候选人资料显示成暂未填写',()=>{
  const html=render({rule:'GRADUATION_REQUIREMENT',result:'UNKNOWN',requirement:'',candidate_value:'2027',explanation:'No sufficiently confident evidence'});
  assert.match(html,/2027 届/);
  assert.match(html,/尚未提取到明确要求/);
  assert.match(html,/可靠岗位要求/);
  assert.doesNotMatch(html,/暂未填写/);
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
test('matching model failures describe analysis and separate transport from evidence validation',()=>{
  const path='/api/matching/analyze';
  assert.ok(D.errorCode('MODEL_CONNECTION_FAILED',502,path).includes('连接失败'));
  assert.ok(D.errorCode('MODEL_RESPONSE_INVALID',502,path).includes('无法读取'));
  assert.ok(D.errorCode('MODEL_ENDPOINT_BLOCKED',502,path).includes('地址解析'));
  assert.ok(D.errorCode('MODEL_TIMEOUT',502,path).includes('模型分析超时'));
  assert.ok(!D.errorCode('MODEL_PROVIDER_FAILED',502,path).includes('草稿'));
  assert.ok(D.errorCode('MATCH_OUTPUT_INVALID',502,path).includes('依据核对'));
});

test('匹配诊断按固定原因说明漏项、引用及能力错误，不显示服务返回的任意文字',()=>{
 assert.match(D.matchingDiagnostic({validation_reason:'MATCH_COUNT',job_index:1,expected:15,actual:9}),/应有 15 项，返回 9 项/);
 assert.match(D.matchingDiagnostic({validation_reason:'EXCERPT_NOT_EXACT',job_index:1,item_index:8}),/结果第 8 项/);
 assert.match(D.matchingDiagnostic({validation_reason:'EXCERPT_NOT_EXACT'}),/连续原文/);
 assert.match(D.matchingDiagnostic({validation_reason:'EXCERPT_ID_UNKNOWN',job_index:1,item_index:2}),/结果第 2 项.*片段编号.*对应资料/);
 assert.match(D.matchingDiagnostic({validation_reason:'EXCERPT_REFERENCE_CONFLICT'}),/同时返回/);
 assert.match(D.matchingDiagnostic({validation_reason:'EXCERPT_AMBIGUOUS'}),/多处原文/);
 assert.match(D.matchingDiagnostic({validation_reason:'FACT_NOT_ABILITY'}),/当成能力证明/);
 assert.equal(D.matchingDiagnostic({validation_reason:'private-fact-or-secret',expected:'private'}),'');
 assert.ok(!D.matchingDiagnostic({validation_reason:'FACT_UNKNOWN',item_index:'private'}).includes('private'));
});
test('匹配容量说明区分个人资料与岗位批次，并显示安全的实际大小',()=>{
 assert.match(D.matchingDiagnostic({capacity_reason:'CANDIDATE_BYTES',actual:33000,limit:32000}),/个人匹配资料共 33,000 字节，上限 32,000 字节/);
 assert.match(D.matchingDiagnostic({capacity_reason:'CANDIDATE_BYTES',actual:33000,limit:32000}),/减少岗位数量不会减少/);
 assert.match(D.matchingDiagnostic({capacity_reason:'COMPARISON_BYTES',actual:55000,limit:54000}),/比较输入共 55,000 字节/);
 assert.equal(D.matchingDiagnostic({capacity_reason:'<script>private text</script>',actual:33000,limit:32000}),'');
 assert.equal(D.matchingDiagnostic({capacity_reason:'constructor',actual:33000,limit:32000}),'');
 assert.equal(D.matchingDiagnostic({capacity_reason:'CANDIDATE_BYTES',actual:'private text',limit:32000}),'');
 assert.doesNotMatch(D.errorCode('MATCH_CAPACITY',400,'/api/matching/preview'),/精简过长的岗位、项目描述/);
});
