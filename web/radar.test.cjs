const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),D=require('./display.js');
const context={CampusDisplay:D,sessionStorage:{getItem:()=>''},document:{querySelector:()=>({addEventListener(){}})},console};context.document.querySelector=()=>({elements:{url:{value:'',addEventListener(){}}},addEventListener(){}});context.document.querySelectorAll=()=>[];context.document.addEventListener=()=>{};
context.URL=URL;vm.createContext(context);vm.runInContext(fs.readFileSync(__dirname+'/ui.js','utf8'),context);vm.runInContext(fs.readFileSync(__dirname+'/source_catalog.js','utf8'),context);vm.runInContext(fs.readFileSync(__dirname+'/source_watches.js','utf8'),context);
const app=fs.readFileSync(__dirname+'/app.js','utf8');vm.runInContext(app.slice(0,app.indexOf("const authUI=")),context);vm.runInContext(fs.readFileSync(__dirname+'/radar.js','utf8'),context);
test('雷达卡片保留来源转义与四个快捷操作',()=>{context.rows=[{job:{id:'a'.repeat(32),company:'<img onerror=x>',title:'后端',current_status:'UNKNOWN',locations:['上海']},eligibility:'UNKNOWN',ranking:{}}];const html=vm.runInContext('radarCards(rows)',context);assert.match(html,/&lt;img/);assert.doesNotMatch(html,/<img/);for(const label of ['稍后看','忽略','准备投递','已投递','暂无法确认'])assert.ok(html.includes(label));});
test('变化记录按业务状态翻译而不推断开放',()=>{context.rows=[{job_id:'a'.repeat(32),type:'JOB_STATUS_CHANGED',from:'OPEN',to:'UNKNOWN',title:'Go',created_at:'2026-09-14T00:00:00Z'}];const html=vm.runInContext('radarChanges(rows)',context);assert.match(html,/岗位状态变化/);assert.match(html,/暂无法确认/);assert.doesNotMatch(html,/UNKNOWN|OPEN/);});
test('登录后进入岗位库并保留原有工作流入口',()=>{assert.match(app,/if\(token\)page\('matching'\)/);const html=fs.readFileSync(__dirname+'/index.html','utf8');for(const page of ['radar','watches','notifications','matching','applications','interviews','agent'])assert.ok(html.includes(`data-page="${page}"`));});
test('home deadline panel stays visible when the optional legacy digest cannot read a large catalog',async()=>{
 let html='',mounted=false;const calls=[],host={isConnected:true};context.page=()=>{};
 context.api=async path=>{calls.push(path);if(path==='/api/radar/todos')return {items:[],counts:{}};throw Error('动态聚合容量限制');};
 context.CampusDeadlines={mount:box=>{assert.equal(box,host);mounted=true;return {dispose(){}};}};context.CampusTodos={mount(){}};
 await context.radarPage('radar',value=>(html=value,true),{querySelector:()=>host,querySelectorAll:()=>[]},'');
 assert.match(html,/id="home-deadlines"/);assert.ok(html.indexOf('home-deadlines')<html.indexOf('today-todos'));assert.equal(mounted,true);assert.match(html,/动态聚合容量限制/);assert.equal(calls.includes('/api/notifications/refresh'),false);delete context.CampusDeadlines;delete context.CampusTodos;
});
test('关注源提供网址预览和分页岗位入口',async()=>{
  let html='';const box={querySelectorAll:()=>[],querySelector:()=>({addEventListener(){}})};
  context.api=async path=>path==='/api/watches'?[]:path==='/api/sources'?[]:path.includes('/jobs?page=')?{total:1,page_size:50,jobs:[{id:'a'.repeat(32),title:'<script>alert(1)</script>',company:'小红书',locations:['上海'],current_status:'UNKNOWN'}]}:null;
  await context.radarPage('watches',value=>(html=value,true),box,'');
  assert.match(html,/source-preview/);assert.match(html,/公司校招网址/);assert.match(html,/刷新进度/);
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
 for(const adapter of ['tencent','ths','cmbnt','netease_game','leihuo','ctyun','ctcloud','tcl_digital','tcl_honghu','cec_software','h3c','hand','beisen_hr','nrec','hec','transsion','sczq_securities']){
  let html='';context.api=async path=>path==='/api/sources'?[{id:'sector-source',adapter,minimum_interval:1800,discovery_supported:true,name:'来源测试'}]:[];
  await context.radarPage('watches',v=>(html=v,true),{querySelectorAll:()=>[]},'');
  assert.match(html,/name="check_interval"[^>]*min="30"/);
 }
});
test('slow progress does not block the watch list, and a superseded or abandoned render ignores late responses',async()=>{
 let html='',alive=true,finished=false;const releases=[],updates=[];
 const elements={'#watch-create':{elements:{source_id:{value:'slow-source'},check_interval:{}}},'#refresh-watches':{},'#source-preview':{elements:{url:{value:'',addEventListener(){}}},addEventListener(){}},'#notice':{}};
 context.document.querySelector=s=>elements[s]||{addEventListener(){}};
 context.api=async path=>path==='/api/watches'?[{id:'slow-watch',source_id:'slow-source',enabled:true,last_outcome:'PROCESSING',check_interval:3600}]:path==='/api/sources'?[{id:'slow-source',name:'慢来源',adapter:'beisen_hr',minimum_interval:1800}]:path.endsWith('/progress')?new Promise(resolve=>releases.push(resolve)):[];
 const box={querySelector:()=>null,querySelectorAll:()=>[]},old=context.CampusSourceWatches.progress;context.CampusSourceWatches.progress=(_,id,value)=>updates.push({id,value});
 try{
  const first=context.radarPage('watches',value=>(html=value,true),box,'',{active:()=>alive}).then(()=>{finished=true;});
  await new Promise(resolve=>setImmediate(resolve));assert.equal(finished,true,'list must become usable while progress is pending');assert.match(html,/慢来源/);assert.equal(releases.length,1);await first;
  elements['#refresh-watches'].onclick();await new Promise(resolve=>setImmediate(resolve));assert.equal(releases.length,2);
  releases[0]({expected:50,completed:1});await new Promise(resolve=>setImmediate(resolve));assert.equal(updates.length,0,'old render must not write into the refreshed list');
  releases[1]({expected:50,completed:2});await new Promise(resolve=>setImmediate(resolve));assert.equal(updates.length,1);assert.equal(updates[0].value.completed,2);
  elements['#refresh-watches'].onclick();await new Promise(resolve=>setImmediate(resolve));alive=false;releases[2]({expected:50,completed:3});await new Promise(resolve=>setImmediate(resolve));assert.equal(updates.length,1,'leaving must prevent late progress writes');
 }finally{for(const release of releases)release(null);context.CampusSourceWatches.progress=old;}
});
