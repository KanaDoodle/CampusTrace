const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(__dirname+'/app.js','utf8');
function harness(){
 const calls=[],elements=new Map(),el=()=>({innerHTML:'',textContent:'',classList:{remove(){}},setAttribute(){}}),document={title:'',querySelector:s=>{if(!elements.has(s))elements.set(s,el());return elements.get(s);},querySelectorAll:()=>[]};
 const c={document,$:document.querySelector,CampusNavigation:{leave:async()=>true},CampusUI:{closeAll(){},heading:s=>s,loading:()=>'',reveal(){}},CampusAssets:{ensure:async route=>calls.push({route})},CampusPreferences:{page:async(_set,options)=>calls.push({preferences:options})},CampusApplications:{page:async(_set,_heading,options)=>calls.push({applications:options})},CampusInterviews:{page:async(_set,_heading,options)=>calls.push({interviews:options})},scheduleInterview(){},reviewInterview(){},formAction(){},table(){},CampusMatching:{page:async(_set,_heading,options)=>calls.push({matching:options})},api:()=>{},esc:v=>v,D:{},UserError:Error,detail:()=>{},pageVersion:0,pageTitles:{preferences:'稍后看',matching:'岗位雷达'}};
 vm.createContext(c);vm.runInContext(source.slice(source.indexOf('async function page('),source.indexOf('\nfunction protectEditor(')),c);return {calls,start:(name,query)=>c.page(name,query)};
}
test('the saved sidebar route loads the independent page rather than the legacy or matching catalog',async()=>{const h=harness();await h.start('preferences');assert.equal(h.calls[0].route,'preferences');assert.ok(h.calls[1].preferences);assert.equal(h.calls.length,2);});
test('the shared-selection action reaches radar with its explicit selected-only context',async()=>{const h=harness();await h.start('matching',{selected:true});assert.equal(h.calls[0].route,'matching');assert.equal(h.calls[1].matching.initialSelected,true);assert.equal(h.calls[1].matching.active(),true);});

test('workflow return routes preserve their explicit resume flag',async()=>{for(const name of ['applications','interviews']){const h=harness();await h.start(name,{resume:true});assert.equal(h.calls[1][name].resume,true);}});
