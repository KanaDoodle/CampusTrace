const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),D=require('./display.js');
function harness({readFile}={}){
  const elements=new Map(),actions=new Map(),writes=[];
  let html='',profile={revision:1,graduation_year:2027,degree:'MASTER',experience_months:3,majors:['Computer Science'],preferred_cities:['Shanghai'],acceptable_cities:['Hangzhou'],target_roles:['后端开发'],technical_skills:['MySQL','Redis'],target_languages:['Go'],preferred_job_types:['FULLTIME']};
  const fields=new Map(Object.entries({...profile,graduation_from:0,graduation_to:0,preferred_job_types:['FULL_TIME']}));
  class FormDataFixture{get(k){const v=fields.get(k);return Array.isArray(v)?D.inputList(v):String(v??'');}getAll(k){return fields.get(k)||[];}}
  const document={querySelector:s=>elements.get(s.slice(1)),querySelectorAll:()=>[],getElementById:id=>elements.get(id)};
  const context={document,FormData:FormDataFixture,CampusProfileLocal:{readFile},CampusModels:{bindUser(){},available:()=>false,label:()=>''},console};
  vm.createContext(context);for(const file of ['ui.js','profile.js'])vm.runInContext(fs.readFileSync(__dirname+'/'+file,'utf8'),context);
  vm.runInContext('this.testProfile=CampusProfile',context);
  const set=value=>{html=value;elements.clear();for(const m of value.matchAll(/\bid="([^"]+)"/g))elements.set(m[1],{id:m[1],getClientRects:()=>[],showModal(){this.open=true;},close(){this.open=false;this.onclose?.();}});elements.set('notice',{textContent:''});return true;};
  const api=async(path,method,body)=>{
    if(path==='/api/profile'){if(method==='PUT'){writes.push(body);profile={...body,revision:profile.revision+1};}return structuredClone(profile);}
    if(path==='/api/projects')return method==='POST'?{id:'new-project',name:body.name}:[];
    if(path==='/api/project_facts')return [];
    if(path==='/api/profile/resume/capabilities')return {user_id:'qa',model_available:false};
    throw new Error(path);
  };
  return {fields,elements,writes,html:()=>html,actions,start:()=>context.testProfile.page(set,'',{api,esc:context.CampusUI.esc,D,formAction:(id,action)=>actions.set(id,action),UserError:Error,navigate(){}}),data:()=>new FormDataFixture()};
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
