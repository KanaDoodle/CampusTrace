const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
function setup(){const stored=new Map(),c={Date,localStorage:{getItem:k=>stored.get(k)||null,setItem:(k,v)=>stored.set(k,v),removeItem:k=>stored.delete(k)}};vm.createContext(c);vm.runInContext(fs.readFileSync(__dirname+'/drafts.js','utf8'),c);return {D:c.CampusDrafts,stored};}
const field=(name,value)=>({name,type:'textarea',value,checked:false});const rows=[{id:'add-project',baseline:[field('description','')],value:[field('description','实现幂等消费')]}];
test('persistent edit drafts require opt in, isolate accounts/scopes and expire after seven days',()=>{
 const {D,stored}=setup();assert.equal(D.write('alice','projects',rows),false);assert.equal(stored.size,0);stored.set('campustrace:local-drafts-enabled:v1:alice','yes');assert.equal(D.write('alice','projects',rows),true);assert.equal(D.load('alice','projects').length,1);assert.equal(D.load('bob','projects').length,0);assert.equal(D.load('alice','review-x').length,0);
 const k='campustrace:local-drafts:v1:alice:projects';stored.set(k,JSON.stringify({updated:Date.now()-8*86400000,records:rows}));assert.equal(D.load('alice','projects').length,0);assert.equal(stored.has(k),false);D.write('alice','projects',[]);assert.equal(stored.has(k),false);
});
test('credentials, raw resume fields and overlarge records cannot be persisted even through the writer',()=>{
 const {D,stored}=setup();stored.set('campustrace:local-drafts-enabled:v1:alice','yes');for(const name of ['password','api_key','resume_text','consent','token'])assert.equal(D.write('alice','projects',[{...rows[0],value:[field(name,'PRIVATE')]}]),false);assert.equal(D.write('alice','projects',[{...rows[0],value:[field('description','a'.repeat(60001))]}]),false);assert.equal([...stored.values()].some(v=>v.includes('PRIVATE')),false);
});

// Exercise the actual form tracker and draft controller together, with a new
// document for each visit. Storage is shared only when simulating a reload.
function mounted({stored=new Map(),version='1',note='已投递',scope='applications',id='application-details-a'}={}){
 const events=new Map(),globalEvents=new Map(),controls=new Map();let markup='';
 const box={get innerHTML(){return markup;},set innerHTML(value){markup=value;controls.clear();for(const attr of value.matchAll(/\b(data-drafts-[a-z]+)(?:\s|>|=)/g))controls.set('['+attr[1]+']',{});},querySelector:selector=>controls.get(selector)||null};
 const form={id,elements:[{name:'record_version',type:'hidden',value:version},{name:'resume_version',type:'text',value:'PRIVATE_RESUME_LABEL'},{name:'note',type:'textarea',value:note}]};
 const document={querySelectorAll:()=>[form],querySelector:selector=>selector==='#local-drafts'?box:form,addEventListener:(name,fn)=>events.set(name,fn),removeEventListener:name=>events.delete(name)};
 const context={document,Date,setTimeout,clearTimeout,localStorage:{getItem:k=>stored.get(k)||null,setItem:(k,v)=>stored.set(k,v),removeItem:k=>stored.delete(k)},addEventListener:(name,fn)=>globalEvents.set(name,fn),removeEventListener:name=>globalEvents.delete(name)};
 vm.createContext(context);for(const name of ['navigation','drafts'])vm.runInContext(fs.readFileSync(__dirname+'/'+name+'.js','utf8'),context);
 const tracker=context.CampusNavigation.forms(document,{ignoreNames:['resume_version']});tracker.restore();
 const controller=context.CampusDrafts.mount({user:'alice',scope,tracker,allow:key=>key===id});
 return {stored,form,tracker,controller,events,globalEvents,markup:()=>markup,click:kind=>controls.get('[data-drafts-'+kind+']').onclick(),message:()=>controls.get('[data-drafts-message]').textContent,enable:()=>controls.get('[data-drafts-enable]').onchange({target:{checked:true}})};
}
test('application notes persist only after opting in and restore without persisting the resume label',()=>{
 const first=mounted();first.form.elements[2].value='明天查看笔试通知';first.controller.flush();assert.match(first.message(),/有未保存/);assert.equal(first.stored.size,0);
 first.enable();assert.match(first.message(),/尚未保存到求职记录/);assert.ok(![...first.stored.values()].join().includes('PRIVATE_RESUME_LABEL'));first.controller.dispose();assert.equal(first.events.size,0);assert.equal(first.globalEvents.has('beforeunload'),false);
 const back=mounted({stored:first.stored});assert.match(back.message(),/发现 1 份/);assert.equal(back.form.elements[2].value,'已投递');back.click('restore');assert.equal(back.form.elements[2].value,'明天查看笔试通知');assert.equal(back.form.elements[1].value,'PRIVATE_RESUME_LABEL');assert.equal(back.tracker.dirty(),true);
 back.tracker.saved('#application-details-a');back.controller.flush();assert.equal(back.stored.has('campustrace:local-drafts:v1:alice:applications'),false);assert.match(back.message(),/没有未保存/);back.controller.dispose();
});
test('application drafts cannot cover a newer record version or current typing',()=>{
 const first=mounted();first.enable();first.form.elements[2].value='旧草稿';first.controller.dispose();
 const newer=mounted({stored:first.stored,version:'2'});newer.click('restore');assert.equal(newer.form.elements[2].value,'已投递');assert.match(newer.message(),/1 份因记录或当前编辑已变化，未覆盖/);assert.match(newer.markup(),/data-drafts-download/);newer.controller.dispose();
 const typing=mounted({stored:first.stored});typing.form.elements[2].value='刚刚输入的笔试结果';typing.click('restore');assert.equal(typing.form.elements[2].value,'刚刚输入的笔试结果');assert.match(typing.message(),/未覆盖/);typing.controller.dispose();
});
test('interview feedback uses an independent scope and saves clear only that scope',()=>{
 const application=mounted();application.enable();application.form.elements[2].value='投递备注';application.controller.dispose();
 const interview=mounted({stored:application.stored,scope:'interview-feedback',id:'interview-result-a'});assert.doesNotMatch(interview.message(),/发现/);interview.form.elements[2].value='复习网络排障';interview.controller.dispose();
 const restored=mounted({stored:application.stored,scope:'interview-feedback',id:'interview-result-a'});restored.click('restore');assert.equal(restored.form.elements[2].value,'复习网络排障');restored.tracker.saved('#interview-result-a');restored.controller.flush();assert.ok(restored.stored.has('campustrace:local-drafts:v1:alice:applications'));assert.equal(restored.stored.has('campustrace:local-drafts:v1:alice:interview-feedback'),false);restored.controller.dispose();
});
