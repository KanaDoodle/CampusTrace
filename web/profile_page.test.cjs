const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),D=require('./display.js');
function harness({readFile,modelAvailable=false,draftResult,initialProfile}={}){
  const elements=new Map(),actions=new Map(),writes=[],draftRequests=[],factWrites=[],projectWrites=[],fields=new Map();
  let html='',profile=initialProfile||{revision:1,graduation_year:2027,degree:'MASTER',experience_months:3,majors:['Computer Science'],preferred_cities:['Shanghai'],acceptable_cities:['Hangzhou'],target_roles:['后端开发'],technical_skills:['MySQL','Redis'],target_languages:['Go'],preferred_job_types:['FULLTIME']},forms=[];
  const decode=v=>String(v||'').replaceAll('&quot;','"').replaceAll('&#39;',"'").replaceAll('&lt;','<').replaceAll('&gt;','>').replaceAll('&amp;','&');
  const attrs=tag=>Object.fromEntries([...tag.matchAll(/([\w-]+)="([^"]*)"/g)].map(m=>[m[1],decode(m[2])]));
  class FormDataFixture{
    constructor(form){this.fields=form?.fields||fields;}
    get(k){const v=this.fields.get(k);return v===undefined?null:Array.isArray(v)?v[0]??null:String(v);}
    getAll(k){const v=this.fields.get(k);return v===undefined?[]:Array.isArray(v)?v:[String(v)];}
    has(k){return this.fields.has(k)&&this.getAll(k).length>0;}
  }
  function parseForm(tag,content){
    const at=attrs(tag),dataset=Object.fromEntries(Object.entries(at).filter(([k])=>k.startsWith('data-')).map(([k,v])=>[k.slice(5).replace(/-([a-z])/g,(_,c)=>c.toUpperCase()),v]));
    const values=at.id==='save-profile'?fields:new Map();values.clear();const controls=[];
    for(const match of content.matchAll(/<input\b[^>]*>|<select\b[^>]*>[\s\S]*?<\/select>|<textarea\b[^>]*>[\s\S]*?<\/textarea>/g)){
      const raw=match[0],a=attrs(raw.split('>')[0]),name=a.name;if(!name)continue;
      const type=raw.startsWith('<select')?'select-one':raw.startsWith('<textarea')?'textarea':a.type||'text';
      let value=a.value||'';
      if(type==='select-one'){const options=[...raw.matchAll(/<option\b([^>]*)>([\s\S]*?)<\/option>/g)];const chosen=options.find(m=>/\bselected\b/.test(m[1]))||options[0];value=chosen?attrs(chosen[1]).value||'':'';}
      if(type==='textarea')value=decode(raw.match(/>([\s\S]*?)<\/textarea>/)[1]);
      if(type==='checkbox'){value=a.value||'on';if(/\bchecked\b/.test(raw))values.set(name,[...(values.get(name)||[]),value]);}
      else if(type==='radio'){if(/\bchecked\b/.test(raw))values.set(name,value);}
      else values.set(name,value);
      const el={name,type};Object.defineProperty(el,'value',{get:()=>['checkbox','radio'].includes(type)?value:String(values.get(name)??''),set:v=>{if(['checkbox','radio'].includes(type))value=v;else values.set(name,v);}});
      Object.defineProperty(el,'checked',{get:()=>type==='checkbox'?(values.get(name)||[]).includes(value):values.get(name)===value,set:v=>{if(type==='checkbox'){const items=(values.get(name)||[]).filter(x=>x!==value);values.set(name,v?[...items,value]:items);}else if(v)values.set(name,value);else if(values.get(name)===value)values.delete(name);}});controls.push(el);
    }
    return {id:at.id||'',dataset,fields:values,elements:controls};
  }
  let approveLeave=false;
  const select=s=>s.startsWith('#')?elements.get(s.slice(1)):forms.find(f=>s.includes('data-draft-project')&&f.dataset.draftProject===s.match(/data-draft-project="([^"']+)"/)?.[1]);
  const document={querySelector:select,querySelectorAll:s=>s==='#content form'?forms:s==='[data-draft-project]'?forms.filter(f=>f.dataset.draftProject!==undefined):s==='[data-edit-fact]'?forms.filter(f=>f.dataset.editFact):s==='[data-edit-project]'?forms.filter(f=>f.dataset.editProject):[],getElementById:id=>elements.get(id)};
  const context={document,CampusDisplay:D,FormData:FormDataFixture,structuredClone,CampusProfileLocal:{readFile,reviewedTextBytes:text=>Buffer.byteLength(text),hasDirectIdentifiers:()=>false},CampusModels:{bindUser(){},available:()=>modelAvailable,label:()=>'',requestConfig:()=>undefined},confirm:()=>approveLeave,console};
  vm.createContext(context);for(const file of ['ui.js','navigation.js','profile_education.js','profile.js'])vm.runInContext(fs.readFileSync(__dirname+'/'+file,'utf8'),context);
  vm.runInContext('this.testProfile=CampusProfile',context);
  const set=value=>{html=value;elements.clear();forms=[];for(const m of value.matchAll(/\bid="([^"]+)"/g))elements.set(m[1],{id:m[1],getClientRects:()=>[],showModal(){this.open=true;},close(){this.open=false;this.onclose?.();}});for(const m of value.matchAll(/(<form\b[^>]*>)([\s\S]*?)<\/form>/g)){const form=parseForm(m[1],m[2]);forms.push(form);if(form.id)elements.set(form.id,form);}elements.set('notice',{textContent:''});return true;};
  const api=async(path,method,body)=>{
    if(path==='/api/profile'){if(method==='PUT'){writes.push(structuredClone(body));profile={...body,revision:profile.revision+1};}return structuredClone(profile);}
    if(path==='/api/projects'){if(method==='POST'){projectWrites.push(body);return {id:'new-project',name:body.name};}return [];}
    if(path==='/api/project_facts'){if(method==='POST'){factWrites.push(body);return {...body,id:'fact-'+factWrites.length};}return [];}
    if(path==='/api/profile/resume/capabilities')return {user_id:'qa',model_available:modelAvailable};
    if(path==='/api/profile/resume/draft'){draftRequests.push(body);return structuredClone(draftResult);}
    throw new Error(path);
  };
  return {navigation:context.CampusNavigation,approve:()=>approveLeave=true,fields,elements,writes,factWrites,projectWrites,draftRequests,html:()=>html,actions,formData:selector=>new FormDataFixture(select(selector)),start:()=>context.testProfile.page(set,'',{api,esc:context.CampusUI.esc,D,formAction:(id,action)=>actions.set(id,action),UserError:Error,navigate(){}}),data:()=>new FormDataFixture()};
}
test('profile keeps every matching field in a single form across hidden sections and recognizes legacy full-time preferences',async()=>{
  const h=harness();await h.start();
  const form=h.html().match(/<form id="save-profile"[^>]*>([\s\S]*?)<\/form>/)[1];
  for(const name of ['education-count','education-0-graduation_year','education-0-degree','education-0-majors','primary_education_id','experience_months','target_roles','preferred_cities','acceptable_cities','target_languages','technical_skills','preferred_job_types'])assert.ok(form.includes(`name="${name}"`),name);
  assert.match(form,/id="profile-preferences"[^>]*hidden/);
  assert.match(form,/value="FULL_TIME" checked/);
});
test('opening resume import preserves unsaved edits and whole-profile save never clears hidden preferences or skills',async()=>{
  const h=harness();await h.start();h.fields.set('education-0-graduation_year','2028');h.fields.set('technical_skills','MySQL、Redis、Linux');
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
  const h=harness();await h.start();h.fields.set('target_roles','后端开发、平台研发');
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
  const h=harness();await h.start();assert.equal(h.navigation.dirty(),false);h.fields.set('target_roles','后端开发、平台研发');assert.equal(await h.navigation.leave(),false);assert.equal(h.navigation.dirty(),true);assert.equal(h.fields.get('target_roles'),'后端开发、平台研发');assert.equal(h.writes.length,0);
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
  assert.match(h.elements.get('notice').textContent,/其余条目已默认勾选/);
});

test('resume byte limit blocks an oversized Chinese preview before any model request',async()=>{
  const h=harness({modelAvailable:true,draftResult:{suggestions:[],projects:[]}});await h.start();const preview=h.elements.get('resume-preview');preview.value='后'.repeat(5334);preview.oninput();const check=h.elements.get('privacy-check');check.checked=true;check.onchange({target:check});
  await h.elements.get('analyze-resume').onclick();assert.equal(h.draftRequests.length,0);assert.match(h.elements.get('notice').textContent,/16000 字节/);assert.equal(h.writes.length,0);
});

async function generate(h){await h.start();const preview=h.elements.get('resume-preview');preview.value='脱敏项目与教育经历原文';preview.oninput();const consent=h.elements.get('privacy-check');consent.checked=true;consent.onchange({target:consent});await h.elements.get('analyze-resume').onclick();}
test('all draft items are selected by default and one project save confirms only the retained selections',async()=>{
 const h=harness({modelAvailable:true,draftResult:{suggestions:[{field:'target_languages',value:'Go',excerpt:'Go'}],projects:[{name:'队列',excerpt:'队列',facts:[{kind:'IMPLEMENTED',claim:'实现重试',excerpt:'实现重试'},{kind:'PLANNED',claim:'添加监控',excerpt:'计划添加监控'},{kind:'LIMITATION',claim:'未压测',excerpt:'未压测'}]}]}});await generate(h);
 assert.deepEqual(h.formData('#apply-suggestions').getAll('pick'),['0']);const form=h.formData('[data-draft-project="0"]');assert.ok(form.has('fact-0'));assert.ok(form.has('fact-1'));assert.ok(form.has('fact-2'));assert.equal(form.has('verified-0'),false);
 assert.match(h.html(),/确认提取无误并保存/);form.fields.delete('fact-1');h.elements.get('open-resume').onclick();const retained=h.formData('[data-draft-project="0"]');assert.equal(retained.has('fact-1'),false);
 await h.actions.get('[data-draft-project="0"]')(retained);assert.equal(h.factWrites.length,2);assert.deepEqual(h.factWrites.map(f=>f.kind),['IMPLEMENTED','LIMITATION']);assert.ok(h.factWrites.every(f=>f.verified));assert.equal(h.projectWrites.length,1);
});
test('education import defaults to both records and selects master graduation without merging majors',async()=>{
 const h=harness({initialProfile:{revision:1,educations:[],technical_skills:['Redis']},modelAvailable:true,draftResult:{suggestions:[],projects:[],educations:[{degree:'BACHELOR',majors:['计算机'],start_year:2020,graduation_year:2024,status:'GRADUATED',excerpt:'本科记录'},{degree:'MASTER',majors:['数学'],start_year:2024,graduation_year:2027,status:'ENROLLED',excerpt:'硕士记录'}]}});await generate(h);
 const data=h.formData('#apply-educations');assert.deepEqual(data.getAll('pick-education'),['0','1']);assert.equal(data.get('education-primary'),'1');assert.equal(h.writes.length,0);
 await h.actions.get('#apply-educations')(data);const saved=h.writes[0];assert.equal(saved.educations.length,2);assert.deepEqual(Array.from(saved.educations[0].majors),['计算机']);assert.deepEqual(Array.from(saved.educations[1].majors),['数学']);assert.equal(saved.degree,'MASTER');assert.equal(saved.graduation_year,2027);assert.equal(saved.primary_education_id,saved.educations[1].id);assert.deepEqual(Array.from(saved.technical_skills),['Redis']);
});
test('education import honors an unchecked record and a changed campus selection',async()=>{
 const h=harness({initialProfile:{revision:1,educations:[]},modelAvailable:true,draftResult:{suggestions:[],projects:[],educations:[{degree:'BACHELOR',majors:['计算机'],start_year:2020,graduation_year:2024,status:'GRADUATED',excerpt:'本科'},{degree:'MASTER',majors:['数学'],start_year:2024,graduation_year:2027,status:'ENROLLED',excerpt:'硕士'}]}});await generate(h);const data=h.formData('#apply-educations');data.fields.set('pick-education',['0']);data.fields.set('education-primary','0');await h.actions.get('#apply-educations')(data);assert.equal(h.writes[0].educations.length,1);assert.equal(h.writes[0].graduation_year,2024);assert.equal(h.writes[0].degree,'BACHELOR');
});
test('manual education rows preserve edits, remain dirty and can all be removed',async()=>{
 const h=harness();await h.start();h.fields.set('target_roles','后端开发、平台研发');h.fields.set('education-0-graduation_year','2028');h.elements.get('add-education').onclick();assert.equal(h.fields.get('education-count'),'2');assert.equal(h.fields.get('education-0-graduation_year'),'2028');assert.equal(h.fields.get('target_roles'),'后端开发、平台研发');assert.equal(h.navigation.dirty(),true);assert.equal(h.writes.length,0);
 h.elements.get('remove-education-0').onclick();assert.equal(h.fields.get('education-count'),'1');h.elements.get('remove-education-0').onclick();assert.equal(h.fields.get('education-count'),'0');await h.actions.get('#save-profile')(h.data());assert.equal(h.writes[0].educations.length,0);assert.equal(h.writes[0].degree,'');assert.equal(h.navigation.dirty(),false);
});

test('saving education retains unrelated unsaved profile edits without silently persisting them',async()=>{
 const h=harness({initialProfile:{revision:1,educations:[],technical_skills:['Redis']},modelAvailable:true,draftResult:{suggestions:[],projects:[],educations:[{degree:'MASTER',majors:['数学'],start_year:2024,graduation_year:2027,status:'ENROLLED',excerpt:'硕士'}]}});await h.start();h.fields.set('technical_skills','Redis、Docker');const preview=h.elements.get('resume-preview');preview.value='脱敏教育经历';preview.oninput();const consent=h.elements.get('privacy-check');consent.checked=true;consent.onchange({target:consent});await h.elements.get('analyze-resume').onclick();await h.actions.get('#apply-educations')(h.formData('#apply-educations'));assert.deepEqual(Array.from(h.writes[0].technical_skills),['Redis']);assert.equal(h.fields.get('technical_skills'),'Redis、Docker');assert.equal(h.navigation.dirty(),true);
});
test('saving a suggestion preserves unsaved manual education rows',async()=>{
 const h=harness({modelAvailable:true,draftResult:{suggestions:[{field:'technical_skills',value:'Linux',excerpt:'Linux'}],projects:[]}});await generate(h);h.elements.get('add-education').onclick();h.fields.set('education-1-majors','软件工程');await h.actions.get('#apply-suggestions')(h.formData('#apply-suggestions'));assert.equal(h.fields.get('education-count'),'2');assert.equal(h.fields.get('education-1-majors'),'软件工程');assert.equal(h.navigation.dirty(),true);assert.equal(h.writes[0].educations,undefined);
});
