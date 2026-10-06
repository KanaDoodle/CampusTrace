const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),D=require('./display.js');
function harness({readFile,modelAvailable=false,draftResult}={}){
  const elements=new Map(),actions=new Map(),writes=[],draftRequests=[];
  let html='',profile={revision:1,graduation_year:2027,degree:'MASTER',experience_months:3,majors:['Computer Science'],preferred_cities:['Shanghai'],acceptable_cities:['Hangzhou'],target_roles:['后端开发'],technical_skills:['MySQL','Redis'],target_languages:['Go'],preferred_job_types:['FULLTIME']};
  const fields=new Map(Object.entries({...profile,graduation_from:0,graduation_to:0,preferred_job_types:['FULL_TIME']}));
  class FormDataFixture{get(k){const v=fields.get(k);return Array.isArray(v)?D.inputList(v):String(v??'');}getAll(k){return fields.get(k)||[];}}
  let approveLeave=false;
  const controls=()=>[...fields].flatMap(([name,value])=>name==='preferred_job_types'?value.map(v=>({name,type:'checkbox',value:v,checked:true})):name==='revision'?[]:[{name,type:name==='degree'?'select-one':'text',value:Array.isArray(value)?D.inputList(value):String(value??'')}]);
  const document={querySelector:s=>elements.get(s.slice(1)),querySelectorAll:s=>s==='#content form'?[elements.get('save-profile')].filter(Boolean):[],getElementById:id=>elements.get(id)};
  const context={document,FormData:FormDataFixture,CampusProfileLocal:{readFile,reviewedTextBytes:text=>Buffer.byteLength(text),hasDirectIdentifiers:()=>false},CampusModels:{bindUser(){},available:()=>modelAvailable,label:()=>'',requestConfig:()=>undefined},confirm:()=>approveLeave,console};
  vm.createContext(context);for(const file of ['ui.js','navigation.js','profile.js'])vm.runInContext(fs.readFileSync(__dirname+'/'+file,'utf8'),context);
  vm.runInContext('this.testProfile=CampusProfile',context);
  const set=value=>{html=value;elements.clear();for(const m of value.matchAll(/\bid="([^"]+)"/g))elements.set(m[1],{id:m[1],getClientRects:()=>[],showModal(){this.open=true;},close(){this.open=false;this.onclose?.();}});Object.defineProperty(elements.get('save-profile'),'elements',{get:controls});elements.set('notice',{textContent:''});return true;};
  const api=async(path,method,body)=>{
    if(path==='/api/profile'){if(method==='PUT'){writes.push(body);profile={...body,revision:profile.revision+1};}return structuredClone(profile);}
    if(path==='/api/projects')return method==='POST'?{id:'new-project',name:body.name}:[];
    if(path==='/api/project_facts')return [];
    if(path==='/api/profile/resume/capabilities')return {user_id:'qa',model_available:modelAvailable};
    if(path==='/api/profile/resume/draft'){draftRequests.push(body);return structuredClone(draftResult);}
    throw new Error(path);
  };
  return {navigation:context.CampusNavigation,approve:()=>approveLeave=true,fields,elements,writes,draftRequests,html:()=>html,actions,start:()=>context.testProfile.page(set,'',{api,esc:context.CampusUI.esc,D,formAction:(id,action)=>actions.set(id,action),UserError:Error,navigate(){}}),data:()=>new FormDataFixture()};
}
test('profile keeps every matching field in a single form across hidden sections and recognizes legacy full-time preferences',async()=>{
  const h=harness();await h.start();
  const form=h.html().match(/<form id="save-profile"[^>]*>([\s\S]*?)<\/form>/)[1];
  for(const name of ['graduation_year','degree','majors','experience_months','target_roles','preferred_cities','acceptable_cities','target_languages','technical_skills','preferred_job_types'])assert.ok(form.includes(`name="${name}"`),name);
  assert.match(form,/id="profile-preferences"[^>]*hidden/);
  assert.match(form,/value="FULL_TIME" checked/);
});
test('opening resume import preserves unsaved edits and whole-profile save never clears hidden preferences or skills',async()=>{
  const h=harness();await h.start();h.fields.set('graduation_year','2028');h.fields.set('technical_skills',['MySQL','Redis','Linux']);
  h.elements.get('open-resume').onclick();
  assert.ok(h.html().includes('value="2028"'));
  assert.ok(h.html().includes('value="MySQL、Redis、Linux"'));
  await h.actions.get('#save-profile')(h.data());
  assert.equal(h.writes[0].graduation_year,2028);
  assert.deepEqual(Array.from(h.writes[0].preferred_cities),['Shanghai']);
  assert.deepEqual(Array.from(h.writes[0].target_languages),['Go']);
  assert.deepEqual(Array.from(h.writes[0].preferred_job_types),['FULL_TIME']);
  assert.deepEqual(Array.from(h.writes[0].technical_skills),['MySQL','Redis','Linux']);
});
test('adding a project preserves edited profile values and does not silently save those edits',async()=>{
  const h=harness();await h.start();h.fields.set('target_roles',['后端开发','平台研发']);
  await h.actions.get('#add-project')({get:()=> '测试项目'});
  assert.ok(h.html().includes('value="后端开发、平台研发"'));
  assert.ok(h.html().includes('测试项目'));
  assert.equal(h.writes.length,0);
});

test('a slow local file read cannot overwrite a newer manual outbound preview',async()=>{
  let finish;const h=harness({readFile:()=>new Promise(resolve=>{finish=resolve;})});await h.start();h.elements.get('open-resume').onclick();
  const reading=h.elements.get('resume-file').onchange({target:{files:[{name:'local.txt'}],value:'local.txt'}});
  const preview=h.elements.get('resume-preview');preview.value='新的手动脱敏文字';preview.oninput();
  finish('过期文件文字');await reading;
  assert.equal(preview.value,'新的手动脱敏文字');
  assert.equal(h.elements.get('analyze-resume').disabled,true);
  assert.equal(h.writes.length,0);
});

test('profile registers a leave guard that retains canceled edits and clears after a successful save',async()=>{
  const h=harness();await h.start();assert.equal(h.navigation.dirty(),false);h.fields.set('target_roles',['后端开发','平台研发']);assert.equal(await h.navigation.leave(),false);assert.equal(h.navigation.dirty(),true);assert.equal(h.fields.get('target_roles')[1],'平台研发');assert.equal(h.writes.length,0);
  await h.actions.get('#save-profile')(h.data());assert.equal(h.navigation.dirty(),false);assert.equal(await h.navigation.leave(),true);
});
test('unsaved local resume text requires a leave warning and never becomes saved profile data',async()=>{
  const h=harness();await h.start();const preview=h.elements.get('resume-preview');preview.value='已脱敏的简历草稿';preview.oninput();assert.equal(await h.navigation.leave(),false);assert.equal(h.writes.length,0);h.approve();assert.equal(await h.navigation.leave(),true);
});

test('partial resume draft shows exclusion reasons and retains valid content without saving it',async()=>{
  const h=harness({modelAvailable:true,draftResult:{suggestions:[{field:'target_languages',value:'Go',excerpt:'Go'}],projects:[{name:'任务队列',excerpt:'任务队列',facts:[{kind:'IMPLEMENTED',claim:'实现失败重试',excerpt:'实现失败重试'}]}],warnings:[{validation_reason:'EXCERPT_NOT_EXACT',scope:'FACT',project_index:1,item_index:2},{validation_reason:'<script>private output</script>',scope:'private output',item_index:3}]}});
  await h.start();const preview=h.elements.get('resume-preview');preview.value='项目任务队列使用 Go 实现失败重试。';preview.oninput();const check=h.elements.get('privacy-check');check.checked=true;check.onchange({target:check});
  await h.elements.get('analyze-resume').onclick();
  assert.equal(h.draftRequests.length,1);assert.equal(h.draftRequests[0].text,'项目任务队列使用 Go 实现失败重试。');
  assert.match(h.html(),/已排除 2 项未通过核对/);assert.match(h.html(),/第 1 个项目的第 2 条事实/);assert.match(h.html(),/实现失败重试/);assert.match(h.html(),/确认草稿/);
  assert.doesNotMatch(h.html(),/private output/);assert.equal(h.writes.length,0);
  assert.match(h.elements.get('notice').textContent,/其余内容可以保存/);
});

test('resume byte limit blocks an oversized Chinese preview before any model request',async()=>{
  const h=harness({modelAvailable:true,draftResult:{suggestions:[],projects:[]}});await h.start();const preview=h.elements.get('resume-preview');preview.value='后'.repeat(5334);preview.oninput();const check=h.elements.get('privacy-check');check.checked=true;check.onchange({target:check});
  await h.elements.get('analyze-resume').onclick();assert.equal(h.draftRequests.length,0);assert.match(h.elements.get('notice').textContent,/16000 字节/);assert.equal(h.writes.length,0);
});
