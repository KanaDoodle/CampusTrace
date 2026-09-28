const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),D=require('./display.js');
const E=require('./evidence.js');
test('guided evidence includes only user-entered work and refuses stale or already supported requirements',()=>{
 assert.equal(E.claimFrom({work:'实现任务认领',method:'MySQL 行锁',outcome:'',job_text:'掌握所有技术'}),'实际工作：实现任务认领\n实现方法：MySQL 行锁');
 const v={state:'ANALYZED',result:{input_key:'key',requirements:[{id:'r',category:'REQUIRED'}],matches:[{requirement_id:'r',result:'NO_EVIDENCE'}]}};
 assert.equal(E.requirementFor(v,'r').id,'r');assert.equal(E.requirementFor({...v,state:'STALE'},'r'),null);
 for(const status of ['DIRECT','PARTIAL','TRANSFERABLE']){v.result.matches[0].result=status;assert.equal(E.requirementFor(v,'r'),null);}
});
function harness({stale=false,noProjects=false,factFail=false}={}){
 const elements=new Map(),actions=new Map(),calls=[],navigation=[];let html='';
 const fields={work:'实现任务认领',method:'MySQL 行锁',outcome:'课程项目',reference:'个人项目文档',verified:'on',project_id:noProjects?'':'p',project_name:'新项目'};
 const data=()=>({get:k=>fields[k],has:k=>Object.hasOwn(fields,k),[Symbol.iterator]:function*(){yield* Object.entries(fields);}});
 class FD{constructor(){return data();}}
 const context={document:{querySelector:s=>elements.get(s.slice(1)),querySelectorAll:()=>[]},FormData:FD,TextEncoder,URL,CampusProfileLocal:{hasDirectIdentifiers:s=>s.includes('@')},console};vm.createContext(context);for(const name of ['ui.js','evidence.js'])vm.runInContext(fs.readFileSync(__dirname+'/'+name,'utf8'),context);vm.runInContext('this.E=CampusEvidence',context);
 let resultReads=0;const v={state:'ANALYZED',result:{input_key:'key',requirements:[{id:'r',category:'REQUIRED',text:'数据库并发控制',excerpt:'岗位原文要求'}],matches:[{requirement_id:'r',result:'NO_EVIDENCE'}]}};
 const api=async(path,method,body)=>{calls.push({path,method,body});if(path.startsWith('/api/matching/results/')){resultReads++;return stale&&resultReads>1?{...v,state:'STALE'}:structuredClone(v);}if(path==='/api/projects')return method==='POST'?{id:'new-p',name:body.name}:noProjects?[]:[{id:'p',name:'已有项目'}];if(path==='/api/project_facts'){if(method==='POST'&&factFail){factFail=false;throw Error('保存失败');}return [];}if(path.startsWith('/api/jobs/'))return {job:{company:'测试公司',title:'后端开发'}};throw Error(path);};
 const set=s=>{html=s;elements.clear();for(const m of s.matchAll(/\bid="([^"]+)"/g))elements.set(m[1],{innerHTML:'',value:'',textContent:'',querySelector:()=>({required:false})});elements.set('notice',{textContent:''});return true;};
 return {fields,calls,actions,navigation,html:()=>html,data,start:()=>context.E.page(set,'',{api,esc:context.CampusUI.esc,D,formAction:(id,cb)=>actions.set(id,cb),UserError:Error,navigate:(...args)=>navigation.push(args)}, {jobID:'j',requirementID:'r',identity:{model_url:'url',model_name:'model',api_key:'must-not-forward'}}),elements};
}
test('saving a confirmed fact rereads current inputs, preserves personal source and never calls a model',async()=>{
 const h=harness();await h.start();assert.ok(h.html().includes('岗位原文要求'));assert.ok(!h.html().includes('required placeholder="掌握'));
 delete h.fields.verified;await assert.rejects(h.actions.get('#evidence-form')(h.data()),/核对/);assert.equal(h.calls.filter(c=>c.method==='POST'&&c.path==='/api/project_facts').length,0);
 h.fields.verified='on';await h.actions.get('#evidence-form')(h.data());
 const write=h.calls.find(c=>c.path==='/api/project_facts'&&c.method==='POST');assert.equal(write.body.verified,true);assert.equal(write.body.kind,'IMPLEMENTED');assert.equal(write.body.reference,'个人项目文档');assert.ok(!write.body.claim.includes('岗位原文要求'));
 assert.ok(h.html().includes('项目事实已保存并确认'));assert.ok(h.html().includes('不会自动调用模型'));assert.ok(!JSON.stringify(h.calls).includes('must-not-forward'));assert.ok(!h.calls.some(c=>c.path==='/api/matching/analyze'));assert.deepEqual(h.navigation,[]);
 h.elements.get('evidence-analyze').onclick();assert.equal(h.navigation[0][0],'matching');assert.equal(h.navigation[0][1].analyze,true);
});
test('changed analysis blocks writes and a failed fact save reuses its newly created project',async()=>{
 const changed=harness({stale:true});await changed.start();await assert.rejects(changed.actions.get('#evidence-form')(changed.data()),/已变化/);assert.ok(!changed.calls.some(c=>c.path==='/api/project_facts'&&c.method==='POST'));
 const partial=harness({noProjects:true,factFail:true});await partial.start();await assert.rejects(partial.actions.get('#evidence-form')(partial.data()),/保存失败/);partial.fields.project_id='new-p';await partial.actions.get('#evidence-form')(partial.data());assert.equal(partial.calls.filter(c=>c.path==='/api/projects'&&c.method==='POST').length,1);
});
