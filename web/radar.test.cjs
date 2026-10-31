const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),D=require('./display.js');
const context={CampusDisplay:D,sessionStorage:{getItem:()=>''},document:{querySelector:()=>({addEventListener(){}})},console};context.document.querySelector=()=>({elements:{url:{value:'',addEventListener(){}}},addEventListener(){}});context.document.querySelectorAll=()=>[];context.document.addEventListener=()=>{};
context.URL=URL;vm.createContext(context);vm.runInContext(fs.readFileSync(__dirname+'/ui.js','utf8'),context);vm.runInContext(fs.readFileSync(__dirname+'/source_catalog.js','utf8'),context);vm.runInContext(fs.readFileSync(__dirname+'/source_watches.js','utf8'),context);
const app=fs.readFileSync(__dirname+'/app.js','utf8');vm.runInContext(app.slice(0,app.indexOf("const authUI=")),context);vm.runInContext(fs.readFileSync(__dirname+'/radar.js','utf8'),context);
test('雷达卡片保留来源转义与四个快捷操作',()=>{context.rows=[{job:{id:'a'.repeat(32),company:'<img onerror=x>',title:'后端',current_status:'UNKNOWN',locations:['上海']},eligibility:'UNKNOWN',ranking:{}}];const html=vm.runInContext('radarCards(rows)',context);assert.match(html,/&lt;img/);assert.doesNotMatch(html,/<img/);for(const label of ['稍后看','忽略','准备投递','已投递','暂无法确认'])assert.ok(html.includes(label));});
test('变化记录按业务状态翻译而不推断开放',()=>{context.rows=[{job_id:'a'.repeat(32),type:'JOB_STATUS_CHANGED',from:'OPEN',to:'UNKNOWN',title:'Go',created_at:'2026-09-14T00:00:00Z'}];const html=vm.runInContext('radarChanges(rows)',context);assert.match(html,/岗位状态变化/);assert.match(html,/暂无法确认/);assert.doesNotMatch(html,/UNKNOWN|OPEN/);});
test('登录后保留核心入口，站内提醒从今日待办进入',()=>{assert.match(app,/if\(token\)page\('matching'\)/);const html=fs.readFileSync(__dirname+'/index.html','utf8');for(const page of ['radar','watches','matching','applications','interviews','agent'])assert.ok(html.includes(`data-page="${page}"`));assert.ok(fs.readFileSync(__dirname+'/todos.js','utf8').includes("go('notifications')"));});
function homeFixture(request){
 let html='',alive=true,guard,todoMounts=0,deadlineMounts=0;const calls=[];
 const node=()=>({innerHTML:'',textContent:'',open:false,isConnected:true,controls:new Map(),querySelector(s){return this.controls.get(s)||null;},querySelectorAll:()=>[]});
 const nodes=new Map(['#home-deadlines','#today-todos','#radar-discoveries','#radar-discovery-content','[data-discovery-summary]'].map(id=>[id,node()]));
 nodes.get('#today-todos').controls.set('[data-reload-todos]',{});nodes.get('#radar-discovery-content').controls.set('[data-refresh-discoveries]',{});
 context.api=async path=>{calls.push(path);return request(path);};context.page=()=>{};
 context.CampusDeadlines={mount:()=>{deadlineMounts++;return {dispose(){}};}};context.CampusTodos={mount:()=>todoMounts++};context.CampusNavigation={register:value=>guard=value};
 return {calls,nodes,start:()=>context.radarPage('radar',value=>(html=value,true),{querySelector:s=>nodes.get(s),querySelectorAll:()=>[]},'',{active:()=>alive}),html:()=>html,deadlineMounts:()=>deadlineMounts,todoMounts:()=>todoMounts,leave:()=>{alive=false;guard?.dispose();delete context.CampusNavigation;delete context.CampusDeadlines;delete context.CampusTodos;},toggle:async()=>{const el=nodes.get('#radar-discoveries');el.open=true;el.ontoggle();await new Promise(setImmediate);},refresh:()=>nodes.get('#radar-discovery-content').controls.get('[data-refresh-discoveries]').onclick()};
}
const digest=()=>({as_of:'2026-10-18T00:00:00Z',counts:{new_jobs:8,recommended_jobs:2,closing_soon:3,status_changes:0,upcoming_interviews:0},new_jobs:[],recommended_jobs:[],recent_changes:[]});
test('home reads deadlines and todos immediately, leaving optional discoveries idle until opened',async()=>{
 const h=homeFixture(async path=>{if(path==='/api/radar/todos')return {items:[],counts:{}};throw Error('动态聚合容量限制');});await h.start();
 assert.match(h.html(),/id="home-deadlines"/);assert.ok(h.html().indexOf('home-deadlines')<h.html().indexOf('today-todos'));assert.equal(h.deadlineMounts(),1);assert.equal(h.todoMounts(),1);assert.deepEqual(h.calls,['/api/radar/todos']);
 await h.toggle();assert.match(h.nodes.get('#radar-discovery-content').innerHTML,/动态聚合容量限制/);assert.equal(h.deadlineMounts(),1);assert.equal(h.calls.includes('/api/notifications/refresh'),false);h.leave();
});
test('slow todos and discoveries never delay each other or replace the deadline region',async()=>{
 let resolveTodos,resolveDigest;const h=homeFixture(path=>new Promise(resolve=>{if(path==='/api/radar/todos')resolveTodos=resolve;else resolveDigest=resolve;}));
 const opening=h.start();assert.equal(h.deadlineMounts(),1);assert.match(h.html(),/正在读取待办/);await h.toggle();assert.equal(typeof resolveDigest,'function');assert.equal(h.todoMounts(),0);
 resolveDigest(digest());await new Promise(setImmediate);assert.match(h.nodes.get('#radar-discovery-content').innerHTML,/今天最值得投/);assert.equal(h.todoMounts(),0);resolveTodos({items:[],counts:{}});await opening;assert.equal(h.todoMounts(),1);assert.equal(h.deadlineMounts(),1);h.leave();
});
test('discovery reads deduplicate, preserve a prior result on failure and ignore replies after navigation',async()=>{
 let resolve,mode='slow';const h=homeFixture(async path=>path==='/api/radar/todos'?{items:[],counts:{}}:mode==='fail'?Promise.reject(Error('合成断网')):new Promise(done=>resolve=done));await h.start();
 await h.toggle();await h.toggle();assert.equal(h.calls.filter(p=>p==='/api/radar/digest').length,1);resolve(digest());await new Promise(setImmediate);assert.match(h.nodes.get('[data-discovery-summary]').textContent,/新增 8/);
 await h.toggle();assert.equal(h.calls.filter(p=>p==='/api/radar/digest').length,1,'reopening a loaded panel reuses the current result');mode='fail';await h.refresh();assert.match(h.nodes.get('#radar-discovery-content').innerHTML,/合成断网/);assert.match(h.nodes.get('#radar-discovery-content').innerHTML,/保留上次/);assert.match(h.nodes.get('#radar-discovery-content').innerHTML,/今天最值得投/);
 mode='slow';const retry=h.refresh();const before=h.nodes.get('#radar-discovery-content').innerHTML;h.leave();resolve(digest());await retry;assert.equal(h.nodes.get('#radar-discovery-content').innerHTML,before);
});
test('todo retry touches only todos and late results cannot mount into another page',async()=>{
 let fail=true,resolve;const h=homeFixture(async()=>fail?Promise.reject(Error('合成待办失败')):new Promise(done=>resolve=done));await h.start();assert.match(h.nodes.get('#today-todos').innerHTML,/合成待办失败/);fail=false;
 const retry=h.nodes.get('#today-todos').controls.get('[data-reload-todos]').onclick();h.leave();resolve({items:[],counts:{}});await retry;assert.equal(h.todoMounts(),0);assert.equal(h.deadlineMounts(),1);assert.equal(h.calls.length,2);
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

test('radar job links open the unified matching detail and source-only dates retain the original day',async()=>{
 const calls=[],button={dataset:{job:'deadline-job'}};context.page=async(name,query)=>calls.push({name,query});context.detail=async()=>{throw Error('旧详情不应打开');};
 context.bindRadar({querySelectorAll:selector=>selector==='[data-job]'?[button]:[]},async()=>{});await button.onclick();assert.equal(calls[0].name,'matching');assert.equal(calls[0].query.jobID,'deadline-job');assert.equal(calls[0].query.view,'overview');
 context.rows=[{job:{id:'deadline-job',company:'虚构公司',title:'Go',current_status:'OPEN',locations:[]},eligibility:'UNKNOWN',deadline:'2026-10-20T00:00:00+08:00',deadline_date:'2026-10-19'}];
 const html=vm.runInContext('radarCards(rows)',context);assert.match(html,/2026-10-19 当天结束/);assert.doesNotMatch(html,/2026年10月20日/);
});

test('radar cards reflect saved and existing application state without suggesting duplicate creation',()=>{
 context.rows=[{job:{id:'a',title:'Go',company:'甲',locations:[],current_status:'OPEN'},disposition:'SAVED',eligibility:'UNKNOWN',application:{id:'app',current_state:'PLANNED',version:4}}];
 const html=vm.runInContext('radarCards(rows)',context);assert.match(html,/取消稍后看/);assert.match(html,/查看投递计划/);assert.match(html,/data-app-version="4"/);assert.doesNotMatch(html,/data-apply="PLANNED"/);assert.doesNotMatch(html,/资格暂无法判断/);
 context.rows[0].application.current_state='APPLIED';const submitted=vm.runInContext('radarCards(rows)',context);assert.match(submitted,/查看投递进展/);assert.doesNotMatch(submitted,/data-apply=/);
});
function actionFixture(button,{request=async()=>({}),active=()=>true,pending=new Set()}={}){
 const calls=[],errors=[],notice={textContent:''};let refreshes=0;
 context.document.querySelector=()=>notice;context.api=async(path,method,body)=>{calls.push({path,method,body});return request(path,body);};context.fail=error=>errors.push(error.message);
 const box={querySelectorAll:selector=>selector==='[data-id]'||selector==='[data-apply]'?[button]:[]};
 context.bindRadar(box,async()=>refreshes++,{active,pending});return {calls,errors,notice,refreshes:()=>refreshes};
}
test('quick application actions use the observed application version or create directly, without a capped list lookup',async()=>{
 const existing={dataset:{id:'job',apply:'APPLIED',appId:'app',appVersion:'4'},disabled:false},a=actionFixture(existing);await existing.onclick();assert.equal(a.calls.length,1);assert.equal(a.calls[0].path,'/api/applications/transition');assert.equal(a.calls[0].body.version,4);assert.equal(a.calls[0].body.application_id,'app');assert.equal(a.refreshes(),1);
 const submitted={dataset:{id:'new',apply:'APPLIED'},disabled:false},b=actionFixture(submitted);await submitted.onclick();assert.equal(b.calls.length,1);assert.equal(b.calls[0].path,'/api/applications');assert.equal(b.calls[0].method,'POST');assert.equal(b.calls[0].body.submitted,true);
 const planned={dataset:{id:'plan',apply:'PLANNED'},disabled:false},c=actionFixture(planned);await planned.onclick();assert.equal(c.calls[0].body.job_id,'plan');assert.equal(Object.hasOwn(c.calls[0].body,'submitted'),false);
});
test('quick actions deduplicate repeated clicks and never overwrite another page after navigation',async()=>{
 let resolve,alive=true;const button={dataset:{id:'job',apply:'PLANNED'},disabled:false},h=actionFixture(button,{active:()=>alive,request:()=>new Promise(done=>resolve=done)});
 const request=button.onclick();await button.onclick();assert.equal(h.calls.length,1);assert.equal(button.disabled,true);alive=false;resolve({});await request;assert.equal(h.refreshes(),0);assert.equal(h.notice.textContent,'');assert.equal(h.errors.length,0);
});
test('stale application versions fail without creating another application and retain an actionable retry',async()=>{
 const button={dataset:{id:'job',apply:'APPLIED',appId:'app',appVersion:'4'},disabled:false},h=actionFixture(button,{request:async()=>{throw Error('投递状态已变化，请刷新。');}});await button.onclick();assert.equal(h.calls.length,1);assert.deepEqual(h.errors,['投递状态已变化，请刷新。']);assert.equal(h.refreshes(),0);assert.equal(button.disabled,false);
});
