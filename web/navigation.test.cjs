const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
function harness({storage=true}={}){
  const stored=new Map(),events=new Map();let allow=false,asked=0;
  const context={sessionStorage:{getItem:k=>{if(!storage)throw Error('blocked');return stored.get(k);},setItem:(k,v)=>{if(!storage)throw Error('blocked');stored.set(k,v);}},confirm:()=>{asked++;return allow;},addEventListener:(name,fn)=>events.set(name,fn)};
  vm.createContext(context);vm.runInContext(fs.readFileSync(__dirname+'/navigation.js','utf8'),context);
  return {N:context.CampusNavigation,stored,events,approve:()=>allow=true,asked:()=>asked};
}
test('browsing restores per-account filters and page but never stores credentials, resume text or consent',()=>{
  const h=harness(),state={query:'后端',company:'测试公司',city:'上海',filter:'STALE',tier:'HIGH',sort:'deep',page:3,onlySelected:true,lastJob:'job-1',lastTab:'source',drawerOpen:true,api_key:'secret',resume:'private text',authorized:true};
  h.N.storeBrowse('alice',state);
  const value=h.N.readBrowse('alice');assert.equal(value.page,3);assert.equal(value.city,'上海');assert.equal(value.lastTab,'source');assert.equal(value.drawerOpen,true);
  assert.equal(h.N.readBrowse('bob').query,'');assert.equal(h.N.readBrowse('bob').lastJob,'');
  const saved=[...h.stored.values()].join();for(const value of ['secret','private text','authorized'])assert.ok(!saved.includes(value));
  assert.equal(h.N.readBrowse('alice').query,'后端');
});
test('malformed saved browsing is discarded and blocked storage retains only session memory',()=>{
  const h=harness();h.stored.set('campustrace:job-browse:v1:alice','not JSON');assert.equal(h.N.readBrowse('alice').page,1);
  h.N.storeBrowse('alice',{page:-3,sort:'injected',filter:'<script>',lastTab:'unknown'});assert.equal(h.N.readBrowse('alice').page,1);assert.equal(h.N.readBrowse('alice').sort,'local');assert.equal(h.N.readBrowse('alice').filter,'');
  const blocked=harness({storage:false});blocked.N.storeBrowse('alice',{query:'Go',page:2});assert.equal(blocked.N.readBrowse('alice').query,'Go');assert.equal(blocked.stored.size,0);
});
test('canceling navigation retains edits and guard; approved leave captures browsing and releases guard',async()=>{
  const h=harness();let captures=0,active=true;h.N.register({active:()=>active,dirty:()=>true,checkpoint:()=>captures++});
  assert.equal(await h.N.leave(),false);assert.equal(h.N.dirty(),true);assert.equal(captures,0);assert.equal(h.asked(),1);
  const event={preventDefault(){this.prevented=true;}};h.events.get('beforeunload')(event);assert.equal(event.prevented,true);assert.equal(event.returnValue,'');
  h.approve();assert.equal(await h.N.leave(),true);assert.equal(h.N.dirty(),false);assert.equal(captures,2);
  h.N.register({active:()=>active,dirty:()=>true});active=false;assert.equal(await h.N.leave(),true);assert.equal(h.asked(),2);
});
test('saving one form preserves edits elsewhere through rerender and reverting removes the leave warning',()=>{
  const h=harness();let forms=[{id:'profile',elements:[{name:'city',type:'text',value:'上海'}]},{dataset:{editFact:'fact-1'},elements:[{name:'claim',type:'textarea',value:'已实现重试'},{name:'verified',type:'checkbox',value:'on',checked:true}]}];
  const doc={querySelectorAll:()=>forms,querySelector:s=>forms.find(f=>f.id===s.slice(1))},tracker=h.N.forms(doc);tracker.restore();
  forms[0].elements[0].value='杭州';forms[1].elements[0].value='已实现幂等重试';assert.equal(tracker.dirty(),true);
  tracker.saved('#profile');tracker.capture();forms=[{id:'profile',elements:[{name:'city',type:'text',value:'杭州'}]},{dataset:{editFact:'fact-1'},elements:[{name:'claim',type:'textarea',value:'已实现重试'},{name:'verified',type:'checkbox',value:'on',checked:true}]}];tracker.restore();
  assert.equal(forms[1].elements[0].value,'已实现幂等重试');assert.equal(tracker.dirty(),true);assert.equal(h.stored.size,0);
  forms[1].elements[0].value='已实现重试';assert.equal(tracker.dirty(),false);
});
test('the in-page leave dialog keeps canceled edits and allows only one pending destination',async()=>{
  const buttons={stay:{focus(){}},leave:{}},trigger={isConnected:true,focus(){this.focused=true;}};
  const dialog={querySelector:s=>s.includes('stay')?buttons.stay:buttons.leave,showModal(){this.open=true;},close(value){this.open=false;this.returnValue=value;this.onclose();}};
  const context={document:{getElementById:()=>dialog,activeElement:trigger}};vm.createContext(context);vm.runInContext(fs.readFileSync(__dirname+'/navigation.js','utf8'),context);
  const N=context.CampusNavigation;let captured=0;N.register({dirty:()=>true,checkpoint:()=>captured++});
  const first=N.leave();assert.equal(dialog.open,true);assert.equal(await N.leave(),false);buttons.stay.onclick();assert.equal(await first,false);assert.equal(N.dirty(),true);assert.equal(trigger.focused,true);assert.equal(captured,0);
  const second=N.leave();buttons.leave.onclick();assert.equal(await second,true);assert.equal(captured,1);assert.equal(N.dirty(),false);
});
test('filtering hides edits without discarding them, and saving an earlier submission keeps later typing dirty',()=>{
  const h=harness();let forms=[{id:'application-a',elements:[{name:'note',type:'textarea',value:'已投递'}]}];
  const doc={querySelectorAll:()=>forms,querySelector:s=>forms.find(form=>form.id===s.slice(1))},tracker=h.N.forms(doc,{selector:'#records form',retainMissing:true});tracker.restore();
  forms[0].elements[0].value='等待笔试';const submitted=tracker.savepoint('#application-a');tracker.capture();forms=[];tracker.restore();
  assert.equal(tracker.dirty(),true);forms=[{id:'application-a',elements:[{name:'note',type:'textarea',value:'已投递'}]}];tracker.restore();assert.equal(forms[0].elements[0].value,'等待笔试');
  forms[0].elements[0].value='补充新收到的通知';tracker.saved('#application-a',submitted);tracker.capture();forms=[{id:'application-a',elements:[{name:'note',type:'textarea',value:'等待笔试'}]}];tracker.restore();
  assert.equal(forms[0].elements[0].value,'补充新收到的通知');assert.equal(tracker.dirty(),true);tracker.forget('#application-a');tracker.restore();assert.equal(tracker.dirty(),false);assert.equal(h.stored.size,0);
});
test('discard confirmation leaves the page guard active and blocks competing navigation while prompting',async()=>{
  const h=harness();h.N.register({dirty:()=>true});assert.equal(await h.N.confirmDiscard(),false);assert.equal(h.N.dirty(),true);h.approve();assert.equal(await h.N.confirmDiscard(),true);assert.equal(h.N.dirty(),true);
});
