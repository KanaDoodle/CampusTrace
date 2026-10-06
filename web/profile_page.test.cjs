const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),D=require('./display.js');
function harness({readFile,modelAvailable=false,draftResult,initialProfile,initialProjects=[],initialFacts=[],deleteError,manualDeleteConfirmation=false,factFailureAt=0,beforeProjectWrite}={}){
  const elements=new Map(),actions=new Map(),writes=[],draftRequests=[],factWrites=[],projectWrites=[],deletions=[],confirmations=[],fields=new Map();
  let projects=structuredClone(initialProjects),facts=structuredClone(initialFacts);
  let factAttempts=0;
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
  const select=s=>{
    if(s.startsWith('#'))return elements.get(s.slice(1));
    // Match complete attribute selectors, including stable form keys. Browsers
    // throw on malformed selectors even after the preceding API writes succeed.
    if(!/^(?:\[data-[\w-]+="[^"]*"\])+$/.test(s))throw new SyntaxError('Invalid selector: '+s);
    const attributes=[...s.matchAll(/\[data-([\w-]+)="([^"]*)"\]/g)].map(([,key,value])=>[key.replace(/-([a-z])/g,(_,c)=>c.toUpperCase()),value]);
    return forms.find(form=>attributes.every(([key,value])=>form.dataset[key]===value));
  };
  const document={querySelector:select,querySelectorAll:s=>s==='#content form'?forms:s==='[data-draft-project]'?forms.filter(f=>f.dataset.draftProject!==undefined):s==='[data-edit-fact]'?forms.filter(f=>f.dataset.editFact):s==='[data-edit-project]'?forms.filter(f=>f.dataset.editProject):[],getElementById:id=>elements.get(id)};
  const context={document,CampusDisplay:D,FormData:FormDataFixture,structuredClone,CampusProfileLocal:{readFile,reviewedTextBytes:text=>Buffer.byteLength(text),hasDirectIdentifiers:()=>false},CampusModels:{bindUser(){},available:()=>modelAvailable,label:()=>'',requestConfig:()=>undefined},confirm:message=>{confirmations.push(message);return approveLeave;},console};
  vm.createContext(context);for(const file of ['ui.js','navigation.js','profile_education.js','profile.js'])vm.runInContext(fs.readFileSync(__dirname+'/'+file,'utf8'),context);
  vm.runInContext('this.testProfile=CampusProfile',context);
  const set=value=>{html=value;elements.clear();forms=[];for(const m of value.matchAll(/\bid="([^"]+)"/g))elements.set(m[1],{id:m[1],getClientRects:()=>[],focus(){},showModal(){this.open=true;if(this.id==='profile-delete-confirm'){confirmations.push(elements.get('profile-delete-message').textContent);if(!manualDeleteConfirmation)this.close(approveLeave?'delete':'cancel');}},close(result){if(result!==undefined)this.returnValue=result;this.open=false;this.onclose?.();}});for(const m of value.matchAll(/(<form\b[^>]*>)([\s\S]*?)<\/form>/g)){const form=parseForm(m[1],m[2]);forms.push(form);if(form.id)elements.set(form.id,form);}elements.set('notice',{textContent:''});return true;};
  const api=async(path,method,body)=>{
    if(path==='/api/profile'){if(method==='PUT'){writes.push(structuredClone(body));profile={...body,revision:profile.revision+1};}return structuredClone(profile);}
    if(path==='/api/projects'){if(method==='POST'){projectWrites.push(body);const ordinal=projectWrites.length;await beforeProjectWrite?.(body);const project={...structuredClone(body),id:ordinal===1?'new-project':'new-project-'+ordinal};projects.push(project);return project;}return structuredClone(projects);}
    if(path.startsWith('/api/projects/')&&method==='PUT'){projectWrites.push(body);const project={...structuredClone(body),id:path.split('/').at(-1)};projects=projects.map(old=>old.id===project.id?project:old);return project;}
    if(path==='/api/project_facts'){if(method==='POST'){if(++factAttempts===factFailureAt)throw new Error('事实保存暂时失败');factWrites.push(body);const fact={...structuredClone(body),id:'fact-'+factWrites.length};facts.push(fact);return fact;}return structuredClone(facts);}
    if(method==='DELETE'){
      if(deleteError)throw new Error(deleteError);deletions.push(path);const id=path.split('/').at(-1);
      if(path.startsWith('/api/projects/')){const deleted_facts=facts.filter(f=>f.project_id===id).length;projects=projects.filter(p=>p.id!==id);facts=facts.filter(f=>f.project_id!==id);return {deleted:true,deleted_facts};}
      if(path.startsWith('/api/project_facts/')){facts=facts.filter(f=>f.id!==id);return {deleted:true};}
    }
    if(path==='/api/profile/resume/capabilities')return {user_id:'qa',model_available:modelAvailable};
    if(path==='/api/profile/resume/draft'){draftRequests.push(body);return structuredClone(draftResult);}
    throw new Error(path);
  };
  return {navigation:context.CampusNavigation,approve:()=>approveLeave=true,fields,elements,writes,factWrites,projectWrites,deletions,confirmations,draftRequests,html:()=>html,actions,formData:selector=>new FormDataFixture(select(selector)),start:()=>context.testProfile.page(set,'',{api,esc:context.CampusUI.esc,D,formAction:(id,action)=>actions.set(id,action),UserError:Error,navigate(){}}),data:()=>new FormDataFixture()};
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
  await h.actions.get('#add-project')({get:key=>key==='name'?'测试项目':null});
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

test('resume import preserves the complete overview and wrapped bullets separately from selected evidence',async()=>{
 const description='个人招聘跟踪工具，支持职位收集与投递记录。',bullet='使用 Redis Streams 实现异步任务处理，\n通过消费者组和失败重试提高可靠性。';
 const h=harness({modelAvailable:true,draftResult:{suggestions:[],projects:[{name:'队列',excerpt:'队列',description,bullets:[bullet],facts:[{kind:'IMPLEMENTED',claim:bullet,excerpt:bullet}]}]}});await generate(h);
 const data=h.formData('[data-draft-project="0"]');assert.equal(data.get('description'),description);assert.equal(data.get('bullet-0'),bullet);assert.ok(data.has('fact-0'));assert.match(h.html(),/用于岗位匹配的依据/);
 await h.actions.get('[data-draft-project="0"]')(data);
 assert.deepEqual(Array.from(h.projectWrites[0].bullets),[bullet]);assert.equal(h.projectWrites[0].description,description);assert.equal(h.factWrites[0].claim,bullet);assert.equal(h.factWrites[0].reference,bullet);
 assert.match(h.html(),/project-description/);assert.match(h.html(),/project-bullets/);assert.ok(h.html().includes(bullet));
});
test('import into an existing project merges complete bullets and keeps its name and manual overview',async()=>{
 const h=harness({initialProjects:[{id:'old',name:'原有队列',description:'手工维护的简介',bullets:['原有完整经历']}],modelAvailable:true,draftResult:{suggestions:[],projects:[{name:'队列',excerpt:'队列',bullets:['新增完整经历'],facts:[{kind:'IMPLEMENTED',claim:'新增完整经历',excerpt:'新增完整经历'}]}]}});await generate(h);
 const data=h.formData('[data-draft-project="0"]');data.fields.set('existing','old');await h.actions.get('[data-draft-project="0"]')(data);
 assert.equal(h.projectWrites.length,1);assert.equal(h.projectWrites[0].name,'原有队列');assert.equal(h.projectWrites[0].description,'手工维护的简介');assert.deepEqual(Array.from(h.projectWrites[0].bullets),['原有完整经历','新增完整经历']);assert.equal(h.factWrites[0].project_id,'old');
});
test('project edit retains paragraph breaks, permits clearing a bullet, and never changes saved evidence',async()=>{
 const h=harness({initialProjects:[{id:'old',name:'队列',description:'完整简介',bullets:['第一条完整经历','第二条完整经历']}],initialFacts:[{id:'f',project_id:'old',kind:'IMPLEMENTED',claim:'已确认机制',verified:true}]});await h.start();
 const data=h.formData('[data-edit-project="old"]');data.fields.set('bullet-0','更新后的技术机制，\n以及执行结果。');data.fields.set('bullet-1','');await h.actions.get('[data-edit-project="old"]')(data);
 assert.deepEqual(Array.from(h.projectWrites[0].bullets),['更新后的技术机制，\n以及执行结果。']);assert.equal(h.projectWrites[0].description,'完整简介');assert.equal(h.factWrites.length,0);assert.match(h.html(),/已确认机制/);
});
test('oversized complete Chinese bullets fail before a project or evidence is written',async()=>{
 const h=harness({modelAvailable:true,draftResult:{suggestions:[],projects:[{name:'队列',excerpt:'队列',facts:[{kind:'IMPLEMENTED',claim:'完整实现',excerpt:'完整实现'}]}]}});await generate(h);const data=h.formData('[data-draft-project="0"]');data.fields.set('bullet-0','后'.repeat(667));
 await assert.rejects(h.actions.get('[data-draft-project="0"]')(data),/2000 字节/);assert.equal(h.projectWrites.length,0);assert.equal(h.factWrites.length,0);
});

const deleteFixture={initialProjects:[{id:'one',name:'待清理的队列',description:'重复导入的项目',bullets:['完整经历']},{id:'two',name:'保留的服务'}],initialFacts:[{id:'f1',project_id:'one',kind:'IMPLEMENTED',claim:'重复的实现',verified:true},{id:'f2',project_id:'one',kind:'IMPLEMENTED',claim:'重复的实现',verified:true},{id:'f3',project_id:'two',kind:'IMPLEMENTED',claim:'保留的依据',verified:true}]};
test('project deletion requires confirmation and identifies the associated facts',async()=>{
 const h=harness(deleteFixture);await h.start();await h.elements.get('delete-project-one').onclick();assert.equal(h.deletions.length,0);assert.match(h.html(),/待清理的队列/);assert.match(h.confirmations[0],/及其 2 条事实/);assert.match(h.confirmations[0],/无法恢复/);
});
test('project deletion removes only its facts and retains unsaved profile and other project edits',async()=>{
 const h=harness(deleteFixture);await h.start();h.fields.set('target_roles','后端开发、平台研发');h.formData('[data-edit-project="two"]').fields.set('name','另一项目的未保存修改');h.approve();await h.elements.get('delete-project-one').onclick();
 assert.deepEqual(h.deletions,['/api/projects/one']);assert.doesNotMatch(h.html(),/待清理的队列|重复的实现/);assert.match(h.html(),/保留的依据/);assert.equal(h.formData('[data-edit-project="two"]').get('name'),'另一项目的未保存修改');assert.equal(h.fields.get('target_roles'),'后端开发、平台研发');assert.equal(h.writes.length,0);assert.equal(h.projectWrites.length,0);assert.equal(h.navigation.dirty(),true);assert.match(h.elements.get('notice').textContent,/2 条事实/);
});
test('deleting one duplicate fact retains its project, complete content, and identical sibling',async()=>{
 const h=harness(deleteFixture);await h.start();h.approve();h.formData('[data-edit-fact="f1"]').fields.set('claim','这条即将删除的未保存修改');await h.elements.get('delete-fact-f1').onclick();
 assert.deepEqual(h.deletions,['/api/project_facts/f1']);assert.equal(h.elements.has('delete-fact-f1'),false);assert.equal(h.elements.has('delete-fact-f2'),true);assert.match(h.html(),/重复导入的项目/);assert.match(h.html(),/完整经历/);assert.match(h.html(),/保留的依据/);assert.equal(h.navigation.dirty(),false);assert.equal(h.factWrites.length,0);
});
test('a failed deletion retains saved and unsaved data and permits retry',async()=>{
 const h=harness({...deleteFixture,deleteError:'删除失败，请重试'});await h.start();h.formData('[data-edit-fact="f1"]').fields.set('claim','保留未保存的内容');h.approve();const button=h.elements.get('delete-project-one');await button.onclick();
 assert.equal(h.deletions.length,0);assert.equal(button.disabled,false);assert.match(h.html(),/待清理的队列/);assert.equal(h.formData('[data-edit-fact="f1"]').get('claim'),'保留未保存的内容');assert.match(h.elements.get('notice').textContent,/删除失败/);
});

test('page confirmation keeps deletion pending until an explicit choice and supports canceling it',async()=>{
 const h=harness({...deleteFixture,manualDeleteConfirmation:true});await h.start();const button=h.elements.get('delete-project-one');const deleting=button.onclick();
 assert.equal(button.disabled,true);assert.equal(h.elements.get('profile-delete-confirm').open,true);assert.equal(h.elements.get('profile-delete-title').textContent,'删除项目');assert.match(h.elements.get('profile-delete-message').textContent,/及其 2 条事实/);assert.equal(h.deletions.length,0);
 h.elements.get('profile-delete-cancel').onclick();await deleting;assert.equal(button.disabled,false);assert.equal(h.elements.get('profile-delete-confirm').open,false);assert.equal(h.deletions.length,0);assert.match(h.html(),/待清理的队列/);
});

const importProject=name=>({name,excerpt:name,bullets:['完整实现经历'],facts:[{kind:'IMPLEMENTED',claim:name+'的实现',excerpt:name+'的实现'}]});
test('saving the final reviewed project clears its edited draft and permits leaving without a warning',async()=>{
 const h=harness({modelAvailable:true,draftResult:{suggestions:[],projects:[importProject('队列')]}});await generate(h);
 const data=h.formData('[data-draft-project="0"]');data.fields.set('name','核对后的队列');data.fields.set('claim-0','核对后的完整实现');assert.equal(h.navigation.dirty(),true);
 await h.actions.get('[data-draft-project="0"]')(data);assert.equal(h.factWrites.length,1);assert.equal(h.factWrites[0].claim,'核对后的完整实现');assert.match(h.elements.get('notice').textContent,/项目已保存/);assert.doesNotMatch(h.html(),/data-draft-project=/);assert.equal(h.navigation.dirty(),false);assert.equal(await h.navigation.leave(),true);assert.equal(h.confirmations.length,0);
});
test('saving the first project preserves the next draft edits after its index shifts',async()=>{
 const h=harness({modelAvailable:true,draftResult:{suggestions:[],projects:[importProject('队列'),importProject('缓存')]}});await generate(h);
 h.formData('[data-draft-project="1"]').fields.set('claim-0','缓存项目尚未保存的完整实现');await h.actions.get('[data-draft-project="0"]')(h.formData('[data-draft-project="0"]'));
 assert.equal(h.navigation.dirty(),true);const remaining=h.formData('[data-draft-project="0"]');assert.equal(remaining.get('name'),'缓存');assert.equal(remaining.get('claim-0'),'缓存项目尚未保存的完整实现');
 await h.actions.get('[data-draft-project="0"]')(remaining);assert.equal(h.projectWrites.length,2);assert.equal(h.factWrites.length,2);assert.equal(h.factWrites[1].claim,'缓存项目尚未保存的完整实现');assert.equal(h.navigation.dirty(),false);assert.equal(await h.navigation.leave(),true);
});
test('project import keeps unrelated manual edits unsaved until the profile is saved',async()=>{
 const h=harness({modelAvailable:true,draftResult:{suggestions:[],projects:[importProject('队列')]}});await generate(h);h.fields.set('target_roles','后端开发、平台研发');
 await h.actions.get('[data-draft-project="0"]')(h.formData('[data-draft-project="0"]'));assert.equal(h.writes.length,0);assert.equal(h.fields.get('target_roles'),'后端开发、平台研发');assert.equal(h.navigation.dirty(),true);
 await h.actions.get('#save-profile')(h.data());assert.equal(h.navigation.dirty(),false);assert.equal(await h.navigation.leave(),true);
});
test('a partial fact failure retains the draft and retry saves only its missing facts',async()=>{
 const project=importProject('队列');project.facts.push({kind:'IMPLEMENTED',claim:'实现失败重试',excerpt:'实现失败重试'});
 const h=harness({factFailureAt:2,modelAvailable:true,draftResult:{suggestions:[],projects:[project]}});await generate(h);const selector='[data-draft-project="0"]',data=h.formData(selector);
 await assert.rejects(h.actions.get(selector)(data),/事实保存暂时失败/);assert.equal(h.factWrites.length,1);assert.equal(h.navigation.dirty(),true);assert.match(h.html(),/data-draft-project=/);
 await h.actions.get(selector)(data);assert.equal(h.factWrites.length,2);assert.equal(h.projectWrites[1].name,h.projectWrites[0].name);assert.equal(h.factWrites[0].project_id,h.factWrites[1].project_id);assert.equal(h.navigation.dirty(),false);assert.equal(await h.navigation.leave(),true);
});
test('project drafts completing concurrently clear their own identities after another import shifts indexes',async()=>{
 let finishSecond;const h=harness({modelAvailable:true,draftResult:{suggestions:[],projects:[importProject('队列'),importProject('缓存')]},beforeProjectWrite:body=>body.name==='缓存'?new Promise(resolve=>{finishSecond=resolve;}):undefined});await generate(h);
 const second=h.actions.get('[data-draft-project="1"]')(h.formData('[data-draft-project="1"]'));await h.actions.get('[data-draft-project="0"]')(h.formData('[data-draft-project="0"]'));assert.equal(h.navigation.dirty(),true);finishSecond();await second;
 assert.equal(h.factWrites.length,2);assert.equal(h.navigation.dirty(),false);assert.doesNotMatch(h.html(),/data-draft-project=/);assert.equal(await h.navigation.leave(),true);
});
