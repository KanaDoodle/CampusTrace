const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),D=require('./display.js');
const context={CampusDisplay:D,sessionStorage:{getItem:()=>''},document:{querySelector:()=>({addEventListener(){}})},console};context.document.querySelector=()=>({elements:{url:{value:'',addEventListener(){}}},addEventListener(){}});context.document.querySelectorAll=()=>[];context.document.addEventListener=()=>{};
context.URL=URL;vm.createContext(context);vm.runInContext(fs.readFileSync(__dirname+'/ui.js','utf8'),context);vm.runInContext(fs.readFileSync(__dirname+'/source_catalog.js','utf8'),context);
const app=fs.readFileSync(__dirname+'/app.js','utf8');vm.runInContext(app.slice(0,app.indexOf("formAction('#login'")),context);vm.runInContext(fs.readFileSync(__dirname+'/radar.js','utf8'),context);
test('雷达卡片保留来源转义与四个快捷操作',()=>{context.rows=[{job:{id:'a'.repeat(32),company:'<img onerror=x>',title:'后端',current_status:'UNKNOWN',locations:['上海']},eligibility:'UNKNOWN',ranking:{}}];const html=vm.runInContext('radarCards(rows)',context);assert.match(html,/&lt;img/);assert.doesNotMatch(html,/<img/);for(const label of ['稍后看','忽略','准备投递','已投递','暂无法确认'])assert.ok(html.includes(label));});
test('变化记录按业务状态翻译而不推断开放',()=>{context.rows=[{job_id:'a'.repeat(32),type:'JOB_STATUS_CHANGED',from:'OPEN',to:'UNKNOWN',title:'Go',created_at:'2026-09-14T00:00:00Z'}];const html=vm.runInContext('radarChanges(rows)',context);assert.match(html,/岗位状态变化/);assert.match(html,/暂无法确认/);assert.doesNotMatch(html,/UNKNOWN|OPEN/);});
test('登录后进入岗位库并保留原有工作流入口',()=>{assert.match(app,/if\(token\)page\('matching'\)/);const html=fs.readFileSync(__dirname+'/index.html','utf8');for(const page of ['radar','watches','notifications','matching','applications','interviews','agent'])assert.ok(html.includes(`data-page="${page}"`));});
test('关注源提供网址预览和分页岗位入口',async()=>{
  let html='';const box={querySelectorAll:()=>[],querySelector:()=>({addEventListener(){}})};
  context.api=async path=>path==='/api/watches'?[]:path==='/api/sources'?[]:path.includes('/jobs?page=')?{total:1,page_size:50,jobs:[{id:'a'.repeat(32),title:'<script>alert(1)</script>',company:'小红书',locations:['上海'],current_status:'UNKNOWN'}]}:null;
  await context.radarPage('watches',value=>(html=value,true),box,'');
  assert.match(html,/source-preview/);assert.match(html,/选择下方已接入的公司/);assert.match(html,/刷新进度/);
  await context.radarPage('source_jobs',value=>(html=value,true),box,{sourceID:'source',page:1});
  assert.match(html,/来源岗位/);assert.match(html,/&lt;script&gt;/);assert.doesNotMatch(html,/<script>alert/);
});

test('校招预设能选择准确范围且编辑网址会清除旧预览',async()=>{
  let html='',listener,focused=false;
  const input={value:'',addEventListener(_,fn){listener=fn;},dispatchEvent(){listener();},focus(){focused=true;}};
  const form={elements:{url:input},addEventListener(){}},result={innerHTML:'旧来源预览'},buttons=[0,1,2,3,4,5,6,7,8,9,10].map(i=>({dataset:{campusPreset:String(i)}}));
  const elements={'#source-preview':form,'#source-preview-result':result,'#refresh-watches':{},'#notice':{}};
  context.document.querySelector=s=>elements[s]||{addEventListener(){}};context.Event=Event;
  const catalog=[{company:'小红书',scope:'当前常规应届校招项目',url:'https://job.xiaohongshu.com/campus/position'},{company:'百度',scope:'应届生校招',url:'https://talent.baidu.com/jobs/list?recruitType=GRADUATE'},{company:'美团<script>',scope:'应届生校招',url:'https://zhaopin.meituan.com/web/campus?hiringType=1_1'},{company:'京东',scope:'应届生项目',url:'https://campus.jd.com/#/jobs?type=present'},{company:'网易互联网',scope:'2027届校招',url:'https://campus.163.com/app/job/position?id=103'},{company:'阿里巴巴',scope:'2027届应届生',url:'https://campus-talent.alibaba.com/campus/position?batchId=100000760001'},{company:'哔哩哔哩',scope:'公开应届生岗位',url:'https://jobs.bilibili.com/campus/positions?type=3'},{company:'快手',scope:'2027届应届生',url:'https://campus.kuaishou.cn/recruit/campus/e/#/campus/jobs?recruitSubProjectCodes=20271779425607'},{company:'OPPO',scope:'2027届应届生',url:'https://careers.oppo.com/university/oppo/campus/post?recruitType=Graduate'},{company:'西门子',scope:'中国官网校招分类',url:'https://jobs.siemens.com.cn/siemens/position/index?recruitmentType=CAMPUSRECRUITMENT'},{company:'海尔集团',scope:'2027校园招聘',url:'https://maker.haier.net/client/campusmobile/activity/id/68/fid.html'}];
  context.api=async p=>p==='/api/sources/catalog'?catalog:[];
  await context.radarPage('watches',v=>(html=v,true),{querySelectorAll:s=>s==='[data-campus-preset]'?buttons:[]},'');
  assert.match(html,/已支持的校招来源/);assert.match(html,/美团&lt;script&gt;/);assert.doesNotMatch(html,/<script>/);
  for(const i of [1,3,4,5,6,7,8,9,10]){buttons[i].onclick();assert.equal(input.value,catalog[i].url);} assert.equal(result.innerHTML,'');assert.equal(focused,true);
});
test('新增行业来源遵循校招最低检查间隔',async()=>{
 context.input=(name,label,value,type,attrs)=>`<label>${label}<input name="${name}" type="${type}" value="${value}" ${attrs}></label>`;
 context.document.querySelector=()=>({elements:{url:{value:'',addEventListener(){}},source_id:{value:'sector-source'},check_interval:{}},addEventListener(){}});
 for(const adapter of ['ths','cmbnt','netease_game','leihuo','ctyun','ctcloud','tcl_digital','tcl_honghu','cec_software']){
  let html='';context.api=async path=>path==='/api/sources'?[{id:'sector-source',adapter,discovery_supported:true,name:'来源测试'}]:[];
  await context.radarPage('watches',v=>(html=v,true),{querySelectorAll:()=>[]},'');
  assert.match(html,/name="check_interval"[^>]*min="30"/);
 }
});
