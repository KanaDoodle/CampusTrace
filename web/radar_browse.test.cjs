const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const {esc}=require('./ui.js'),Browse=require('./radar_browse.js');
const records=n=>Array.from({length:n},(_,i)=>({job:{id:'job-'+i,company:i%2?'乙公司':'甲公司',title:'Go 服务端 '+i,locations:['上海']},deadline:'2099-01-01T00:00:00Z'}));
function harness({rows=records(1001),stored=new Map(),user='alice',request,kind='closing'}={}){
 const elements=new Map(),windows=[],calls=[],navigations=[],scrolls=[];let active=true,guard,options,scroll=0;
 const decode=v=>v.replaceAll('&quot;','"').replaceAll('&lt;','<').replaceAll('&gt;','>').replaceAll('&amp;','&');
 function node(id){return {id,value:'',disabled:false,isConnected:true,innerHTML:'',buttons:[],querySelectorAll(s){return s==='[data-job]'?this.buttons:[];},scrollIntoView(){this.scrolled=true;},focus(){this.focused=true;}};}
 function parse(html,all=false){
  if(all){elements.clear();windows.length=0;}
  for(const m of html.matchAll(/\bid="([^"]+)"([^>]*)>/g)){const el=node(m[1]);el.disabled=/\bdisabled\b/.test(m[2]);elements.set(el.id,el);}
  const search=html.match(/<input id="radar-browse-search"[^>]*value="([^"]*)"/);if(search)elements.get('radar-browse-search').value=decode(search[1]);
  if(all)for(const m of html.matchAll(/data-browse-days="(\d+)"/g))windows.push({dataset:{browseDays:m[1]},setAttribute(name,value){this[name]=value;}});
 }
 const context={sessionStorage:{getItem:k=>stored.get(k),setItem:(k,v)=>stored.set(k,v)},document:{querySelector:s=>elements.get(s.slice(1)),querySelectorAll:s=>s==='[data-browse-days]'?windows:[]},scrollTo:v=>{scrolls.push(v.top);scroll=v.top;},CampusUI:{heading:(t,_subtitle,actions)=>`<h1>${t}</h1>${actions}`,reconcile(host,html){host.innerHTML=html;parse(html);host.buttons=[...html.matchAll(/data-job="([^"]+)"/g)].map(m=>({dataset:{job:m[1]},focus(){this.focused=true;}}));}},get scrollY(){return scroll;}};
 vm.createContext(context);for(const name of ['navigation.js','radar_browse.js'])vm.runInContext(fs.readFileSync(__dirname+'/'+name,'utf8'),context);
 const originalRegister=context.CampusNavigation.register;context.CampusNavigation.register=v=>{guard=v;originalRegister(v);};
 const api=async path=>{calls.push(path);if(path==='/api/profile/resume/capabilities')return {user_id:user};return request?request(path):structuredClone(rows);};
 return {elements,windows,calls,navigations,stored,scrolls,nav:context.CampusNavigation,options:()=>options,scroll:y=>scroll=y,leave:async()=>{await context.CampusNavigation.leave();active=false;},html:()=>elements.get('radar-browse-results').innerHTML,start:query=>context.CampusRadarBrowse.page(html=>{parse(html,true);return true;},{kind,query,api,esc,D:{},navigate:async(name,query)=>{await context.CampusNavigation.leave();navigations.push({name,query});},active:()=>active,cards:(rows,{disabled})=>rows.map(v=>`<article data-job="${esc(v.job.id)}" ${disabled?'disabled':''}>${esc(v.job.title)}</article>`).join(''),changes:rows=>rows.map(v=>`<article data-job="${esc(v.job_id)}">${esc(v.title)}</article>`).join(''),bindActions:(host,refresh,value)=>{options=value;for(const b of host.buttons)b.onclick=()=>value.openJob(b.dataset.job);},notify:()=>{}})};
}
test('browse input is bounded and closing filters match company, role and city together',()=>{
 const state=Browse.clean('closing',{days:1,page:-1,query:'Go 上海',company:'甲公司',scroll:Infinity,lastJob:'j'.repeat(300),api_key:'private'});assert.equal(state.days,7);assert.equal(state.page,1);assert.equal(state.scroll,0);assert.equal(state.lastJob.length,128);assert.ok(!JSON.stringify(state).includes('private'));assert.equal(Browse.filter('closing',records(12),state).length,6);
 assert.equal(Browse.clean('changes',{days:3,company:'甲'}).company,'');assert.equal(Browse.clean('changes',{days:1}).days,1);
});
test('a large deadline list renders only one page and filters without another read',async()=>{
 const h=harness();await h.start(7);assert.equal(h.html().match(/<article/g).length,24);assert.match(h.html(),/共 1001 个岗位/);assert.match(h.html(),/第 1 \/ 42 页/);
 h.elements.get('radar-browse-next').onclick();assert.match(h.html(),/第 2 \/ 42 页/);assert.match(h.html(),/Go 服务端 24/);assert.doesNotMatch(h.html(),/>Go 服务端 0</);
 const search=h.elements.get('radar-browse-search');search.value='服务端 1000';search.oninput({});assert.match(h.html(),/符合筛选 1 条/);assert.match(h.html(),/第 1 \/ 1 页/);assert.equal(h.calls.length,2);
});
test('Chinese composition never replaces the search input or renders incomplete text',async()=>{
 const h=harness();await h.start(7);const search=h.elements.get('radar-browse-search'),before=h.html();search.oncompositionstart();search.value='乙公司';search.oninput({isComposing:true});assert.equal(h.html(),before);search.oncompositionend();assert.match(h.html(),/符合筛选 500 条/);assert.equal(h.elements.get('radar-browse-search'),search);h.elements.get('radar-browse-clear').onclick();assert.equal(search.value,'');assert.match(h.html(),/共 1001 个岗位/);
});
test('detail return restores filters, page, position and focused job within the same account',async()=>{
 const stored=new Map(),rows=records(80),h=harness({stored,rows});await h.start(7);const search=h.elements.get('radar-browse-search');search.value='Go';search.oninput({});const company=h.elements.get('radar-browse-company');company.value='甲公司';company.onchange();h.elements.get('radar-browse-next').onclick();h.scroll(620);const button=h.elements.get('radar-browse-results').buttons[0];await button.onclick();
 assert.equal(h.navigations[0].query.returnTo.page,'closing');assert.equal(h.navigations[0].query.jobID,'job-48');
 const resumed=harness({stored,rows});await resumed.start({resume:true});assert.equal(resumed.elements.get('radar-browse-search').value,'Go');assert.equal(resumed.elements.get('radar-browse-company').value,'甲公司');assert.match(resumed.html(),/第 2 \/ 2 页/);assert.equal(resumed.scrolls.at(-1),620);assert.equal(resumed.elements.get('radar-browse-results').buttons[0].focused,true);
 const other=harness({stored,rows,user:'bob'});await other.start({resume:true});assert.equal(other.elements.get('radar-browse-search').value,'');assert.equal(other.elements.get('radar-browse-company').value,'');assert.match(other.html(),/第 1 \/ 4 页/);
});
test('a failed range read retains the verified scope, disables writes and can recover',async()=>{
 let failure=false;const h=harness({request:async()=>{if(failure)throw Error('合成断网');return records(30);}});await h.start(7);failure=true;h.windows.find(b=>b.dataset.browseDays==='14').onclick();await new Promise(setImmediate);assert.match(h.html(),/合成断网/);assert.match(h.html(),/保留未来 7 天/);assert.doesNotMatch(h.html(),/暂无待投/);assert.equal(h.options().canAct(),false);assert.match(h.html(),/data-job="job-0" disabled/);
 failure=false;await h.elements.get('radar-browse-refresh').onclick();assert.equal(h.options().canAct(),true);assert.match(h.html(),/未来 14 天/);assert.doesNotMatch(h.html(),/合成断网/);
});
test('a slow initial read deduplicates reloads and cannot repaint after leaving',async()=>{
 let resolve;const h=harness({request:()=>new Promise(done=>resolve=done)}),opening=h.start(7);await new Promise(setImmediate);await h.elements.get('radar-browse-refresh').onclick();assert.equal(h.calls.length,2);const before=h.html();await h.leave();resolve(records(30));await opening;assert.equal(h.html(),before);
});

test('changes use the same pagination and title search while retaining the server feed limit',async()=>{
 const h=harness({kind:'changes',rows:Array.from({length:100},(_,i)=>({job_id:'j'+i,title:i%2?'算法岗':'服务端岗',type:'NEW_JOB'}))});await h.start(1);assert.match(h.html(),/过去 1 天/);assert.match(h.html(),/最近 100 条/);assert.match(h.html(),/第 1 \/ 5 页/);assert.equal(h.html().match(/<article/g).length,24);const search=h.elements.get('radar-browse-search');search.value='服务端';search.oninput({});assert.match(h.html(),/符合筛选 50 条/);assert.ok(h.calls.includes('/api/radar/changes?days=1'));
});
