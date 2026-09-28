const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),D=require('./display.js');
const context={CampusDisplay:D,sessionStorage:{getItem:()=>''},document:{querySelector:()=>({addEventListener(){}})},console};context.document.querySelectorAll=()=>[];context.document.addEventListener=()=>{};
vm.createContext(context);vm.runInContext(fs.readFileSync(__dirname+'/ui.js','utf8'),context);
const app=fs.readFileSync(__dirname+'/app.js','utf8');vm.runInContext(app.slice(0,app.indexOf("formAction('#login'")),context);vm.runInContext(fs.readFileSync(__dirname+'/radar.js','utf8'),context);
test('雷达卡片保留来源转义与四个快捷操作',()=>{context.rows=[{job:{id:'a'.repeat(32),company:'<img onerror=x>',title:'后端',current_status:'UNKNOWN',locations:['上海']},eligibility:'UNKNOWN',ranking:{}}];const html=vm.runInContext('radarCards(rows)',context);assert.match(html,/&lt;img/);assert.doesNotMatch(html,/<img/);for(const label of ['稍后看','忽略','准备投递','已投递','暂无法确认'])assert.ok(html.includes(label));});
test('变化记录按业务状态翻译而不推断开放',()=>{context.rows=[{job_id:'a'.repeat(32),type:'JOB_STATUS_CHANGED',from:'OPEN',to:'UNKNOWN',title:'Go',created_at:'2026-09-14T00:00:00Z'}];const html=vm.runInContext('radarChanges(rows)',context);assert.match(html,/岗位状态变化/);assert.match(html,/暂无法确认/);assert.doesNotMatch(html,/UNKNOWN|OPEN/);});
test('登录后进入岗位库并保留原有工作流入口',()=>{assert.match(app,/if\(token\)page\('matching'\)/);const html=fs.readFileSync(__dirname+'/index.html','utf8');for(const page of ['radar','watches','notifications','matching','applications','interviews','agent'])assert.ok(html.includes(`data-page="${page}"`));});
test('关注源提供网址预览和分页岗位入口',async()=>{
  let html='';const box={querySelectorAll:()=>[],querySelector:()=>({addEventListener(){}})};
  context.api=async path=>path==='/api/watches'?[]:path==='/api/sources'?[]:path.includes('/jobs?page=')?{total:1,page_size:50,jobs:[{id:'a'.repeat(32),title:'<script>alert(1)</script>',company:'小红书',locations:['上海'],current_status:'UNKNOWN'}]}:null;
  await context.radarPage('watches',value=>(html=value,true),box,'');
  assert.match(html,/source-preview/);assert.match(html,/小红书校招/);assert.match(html,/刷新进度/);
  await context.radarPage('source_jobs',value=>(html=value,true),box,{sourceID:'source',page:1});
  assert.match(html,/来源岗位/);assert.match(html,/&lt;script&gt;/);assert.doesNotMatch(html,/<script>alert/);
});
