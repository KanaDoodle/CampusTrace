const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');

function harness({supportsDirection=false}={}){
 const actions=new Map(),requests=[],registrations=[],elements=new Map();let html='';
 const url='https://talent.baidu.com/jobs/list?recruitType=GRADUATE';
 const input={name:'url',type:'url',value:url,addEventListener(){},focus(){}};
 elements.set('#source-preview',{id:'source-preview',elements:Object.assign([input],{url:input}),dataset:{}});
 elements.set('#source-create',{id:'source-create',elements:[],dataset:{}});
 elements.set('#source-preview-result',{innerHTML:''});elements.set('#refresh-watches',{});elements.set('#notice',{textContent:''});
 const document={querySelector:selector=>elements.get(selector)||null,querySelectorAll:()=>[],addEventListener(){},getElementById:()=>null};
 const context={document,console,URL,Event,structuredClone,sessionStorage:{getItem:()=>''},CampusDisplay:require('./display.js')};
 vm.createContext(context);for(const file of ['ui.js','navigation.js','source_catalog.js'])vm.runInContext(fs.readFileSync(__dirname+'/'+file,'utf8'),context);
 const app=fs.readFileSync(__dirname+'/app.js','utf8');vm.runInContext(app.slice(0,app.indexOf("const authUI=")),context);
 vm.runInContext(app.match(/^function input[^\n]+/m)[0],context);
 vm.runInContext(fs.readFileSync(__dirname+'/radar.js','utf8'),context);
 context.formAction=(selector,action)=>actions.set(selector,action);
 context.api=async(path,method,body)=>{
  if(['/api/watches','/api/sources','/api/sources/catalog'].includes(path))return [];
  if(path==='/api/sources/preview')return {name:'校招测试',url,total:1,samples:[{title:'后端开发'}],supports_direction:supportsDirection,minimum_interval:1800};
  if(path==='/api/sources/from-url'){
   requests.push(structuredClone(body));
   // The real API rejects null fields before creating a source or watch.
   if(Object.values(body).some(value=>value===null))throw new Error('null values are not accepted');
   registrations.push(structuredClone(body));return {existing:false};
  }
  throw new Error(path);
 };
 const data=values=>({get:key=>Object.hasOwn(values,key)?values[key]:null,has:key=>Object.hasOwn(values,key)});
 return {actions,requests,registrations,notice:()=>elements.get('#notice').textContent,html:()=>html,data,url,start:()=>context.radarPage('watches',value=>(html=value,true),{querySelectorAll:()=>[],querySelector:()=>null},'')};
}

test('a source without a direction field imports successfully after preview instead of submitting null',async()=>{
 const h=harness();await h.start();await h.actions.get('#source-preview')(h.data({url:h.url}));
 assert.match(h.html(),/source-create/);assert.doesNotMatch(h.html(),/name="direction"/);
 await h.actions.get('#source-create')(h.data({url:h.url,keyword:'',check_interval:'360',enabled:'on',adaptive:'on'}));
 assert.equal(h.registrations.length,1);assert.deepEqual(h.requests[0],{url:h.url,direction:'',keyword:'',check_interval:21600,enabled:true,adaptive:true,priority:false});assert.match(h.notice(),/已创建关注/);
});
test('a source with a direction selector preserves the selected direction and keyword',async()=>{
 const h=harness({supportsDirection:true});await h.start();await h.actions.get('#source-preview')(h.data({url:h.url}));assert.match(h.html(),/name="direction"/);
 await h.actions.get('#source-create')(h.data({url:h.url,direction:'rd',keyword:'后端',check_interval:'60',enabled:'on',priority:'on'}));
 assert.equal(h.registrations.length,1);assert.equal(h.requests[0].direction,'rd');assert.equal(h.requests[0].keyword,'后端');assert.equal(h.requests[0].check_interval,3600);assert.equal(h.requests[0].adaptive,false);assert.equal(h.requests[0].priority,true);
});
