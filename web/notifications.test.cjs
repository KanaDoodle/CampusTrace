const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const Inbox=require('./notifications.js'),{esc}=require('./ui.js');
const records=n=>Array.from({length:n},(_,i)=>({id:'n'+i,title:'Go 岗位变化 '+i,body:'虚构星海公司 上海',entity_type:'JOB',entity_id:'job-'+i,created_at:'2099-01-01T01:00:00Z',...(i%4===0?{read_at:'2099-01-01T02:00:00Z'}:{})}));
function harness({rows=records(100),user='alice',stored=new Map(),request}={}){
 const elements=new Map(),calls=[],navigations=[],messages=[],scrolls=[];let html='',shells=0,scroll=0,live=true,articles=[];
 const decode=v=>v.replaceAll('&quot;','"').replaceAll('&#39;',"'").replaceAll('&lt;','<').replaceAll('&gt;','>').replaceAll('&amp;','&');
 const document={querySelector:s=>elements.get(s.slice(1)),activeElement:null};
 const node=()=>({value:'',disabled:false,textContent:'',innerHTML:'',setAttribute(k,v){this[k]=v;},focus(){document.activeElement=this;this.focused=true;},scrollIntoView(){this.scrolled=true;}});
 function parse(value,all=false){
  if(all){elements.clear();articles=[];}
  for(const m of value.matchAll(/\bid="([^"]+)"([^>]*)>/g)){const el=node();el.disabled=/\bdisabled\b/.test(m[2]);elements.set(m[1],el);}
  if(all){const search=value.match(/<input id="inbox-search"[^>]*value="([^"]*)"/);elements.get('inbox-search').value=decode(search[1]);elements.get('inbox-results').querySelectorAll=s=>s==='[data-notification]'?articles:s==='[data-notification-read]'?articles.map(a=>a.read).filter(Boolean):s==='[data-notification-job]'?articles.map(a=>a.job).filter(Boolean):[];}
  else{
   const old=new Map(articles.map(a=>[a.dataset.notification,a]));articles=[];
   for(const m of value.matchAll(/<article[^>]*data-notification="([^"]+)"[^>]*>([\s\S]*?)<\/article>/g)){
    const id=decode(m[1]),a=old.get(id)||{...node(),dataset:{notification:id},querySelector(s){return s==='[data-notification-read]'?this.read:this.job;}};
    if(/data-notification-read=/.test(m[2])){a.read||={...node(),dataset:{notificationRead:id}};a.read.disabled=/<button[^>]*data-notification-read="[^"]+"[^>]*\bdisabled\b/.test(m[2]);}else a.read=null;
    const job=m[2].match(/data-job-id="([^"]+)"/);if(job)a.job||={...node(),dataset:{notificationJob:id,jobId:decode(job[1])}};
    articles.push(a);
   }
  }
 }
 const context={document,sessionStorage:{getItem:k=>stored.get(k),setItem:(k,v)=>stored.set(k,v)},get scrollY(){return scroll;},scrollTo:v=>{scroll=v.top;scrolls.push(scroll);},CampusUI:{heading:(title,_subtitle,actions)=>`<h1>${title}</h1>${actions}`,reconcile(host,value){host.innerHTML=value;html=value;parse(value);}}};
 vm.createContext(context);for(const name of ['navigation.js','notifications.js'])vm.runInContext(fs.readFileSync(__dirname+'/'+name,'utf8'),context);
 const api=async(path,method='GET',body)=>{calls.push({path,method,body});if(path==='/api/profile/resume/capabilities')return {user_id:user};if(request)return request(path,method,body);if(path==='/api/notifications')return structuredClone(rows);if(path.endsWith('/read'))return {read:true};if(path.endsWith('/refresh'))return {created:0,deduplicated:0};throw Error(path);};
 return {elements,calls,navigations,messages,stored,scrolls,document,html:()=>html,shells:()=>shells,articles:()=>articles,scroll:y=>scroll=y,leave:async()=>{await context.CampusNavigation.leave();live=false;},start:query=>context.CampusNotifications.page(value=>{shells++;parse(value,true);return true;},{api,esc,D:{date:value=>'时间 '+value},navigate:async(name,query)=>{await context.CampusNavigation.leave();navigations.push({name,query});},query,active:()=>live,notify:message=>messages.push(message)})};
}
test('inbox state is bounded and searches match all words across title and body',()=>{
 const state=Inbox.clean({query:'Go 上海',view:'unread',page:-1,scroll:Infinity,lastNotification:'n'.repeat(300),api_key:'private'});
 assert.equal(state.page,1);assert.equal(state.scroll,0);assert.equal(state.lastNotification.length,128);assert.ok(!JSON.stringify(state).includes('private'));assert.equal(Inbox.filter(records(100),state).length,75);assert.equal(Inbox.clean({view:'bogus'}).view,'all');
});
test('the recent inbox pages locally, searches body text and never generates reminders on entry',async()=>{
 const h=harness();await h.start();assert.equal(h.articles().length,20);assert.match(h.html(),/最近 100 条 · 未读 75 条/);assert.match(h.html(),/第 1 \/ 5 页/);
 h.elements.get('inbox-next').onclick();assert.match(h.html(),/Go 岗位变化 20/);const input=h.elements.get('inbox-search');input.value='变化 99 上海';input.oninput({});assert.match(h.html(),/当前筛选 1 条/);assert.match(h.html(),/Go 岗位变化 99/);assert.equal(h.shells(),1);assert.equal(h.calls.length,2);assert.ok(h.calls.every(v=>v.method==='GET'));
});
test('Chinese composition preserves the search control and waits for complete input',async()=>{
 const h=harness();await h.start();const input=h.elements.get('inbox-search'),before=h.html();input.oncompositionstart();input.value='星海';input.oninput({isComposing:true});assert.equal(h.html(),before);input.oncompositionend();assert.equal(h.elements.get('inbox-search'),input);assert.match(h.html(),/当前筛选 100 条/);h.elements.get('inbox-clear').onclick();assert.equal(input.value,'');
});
test('confirmed reads update the same filtered page without a list reload, clamping the last unread page',async()=>{
 const h=harness({rows:records(21).map(({read_at,...row})=>row)});await h.start();h.elements.get('inbox-unread').onclick();const input=h.elements.get('inbox-search');input.value='Go';input.oninput({});h.elements.get('inbox-next').onclick();const button=h.articles()[0].read;button.focus();await button.onclick();
 assert.match(h.html(),/未读 20 条 · 当前筛选 20 条/);assert.match(h.html(),/第 1 \/ 1 页/);assert.equal(input.value,'Go');assert.equal(h.elements.get('inbox-unread')['aria-pressed'],'true');assert.equal(h.calls.filter(v=>v.path==='/api/notifications').length,1);assert.equal(h.calls.filter(v=>v.method==='POST').length,1);assert.equal(h.shells(),1);assert.equal(h.document.activeElement,h.articles()[0].read);
});
test('duplicate read clicks are deduplicated and failed writes retain unread records for retry',async()=>{
 let resolve,mode='slow';const h=harness({request:async(path)=>path==='/api/notifications'?records(2):mode==='slow'?new Promise(done=>resolve=done):{read:true}});await h.start();h.elements.get('inbox-unread').onclick();const button=h.articles()[0].read,writing=button.onclick();await button.onclick();assert.equal(h.calls.filter(v=>v.method==='POST').length,1);assert.equal(h.elements.get('inbox-refresh').disabled,true);
 resolve({read:false});await writing;assert.match(h.html(),/未能确认已读状态/);assert.match(h.html(),/重试标为已读/);assert.match(h.html(),/当前筛选 1 条/);mode='ready';await h.articles()[0].read.onclick();assert.match(h.html(),/没有未读通知/);assert.equal(h.calls.filter(v=>v.path==='/api/notifications').length,1);
});
test('a pending mark does not steal focus from a search the user started meanwhile',async()=>{
 let resolve;const h=harness({request:async path=>path==='/api/notifications'?records(2):new Promise(done=>resolve=done)});await h.start();const b=h.articles()[1].read;b.focus();const saving=b.onclick(),input=h.elements.get('inbox-search');input.focus();input.value='Go';input.oninput({});resolve({read:true});await saving;assert.equal(h.document.activeElement,input);assert.equal(input.value,'Go');assert.match(h.html(),/未读 0 条/);
});
test('read refresh failures preserve the current page, filter and records, with a read-only retry',async()=>{
 let failed=false;const h=harness({request:async path=>{if(failed)throw Error('合成断网');return records(45);}});await h.start();h.elements.get('inbox-search').value='Go';h.elements.get('inbox-search').oninput({});h.elements.get('inbox-next').onclick();failed=true;await h.elements.get('inbox-refresh').onclick();assert.match(h.html(),/合成断网.*保留上次读取/);assert.match(h.html(),/第 2 \/ 3 页/);assert.equal(h.articles().length,20);assert.ok(h.articles().filter(a=>a.read).every(a=>a.read.disabled));
 failed=false;await h.elements.get('inbox-retry').onclick();assert.doesNotMatch(h.html(),/合成断网/);assert.equal(h.elements.get('inbox-search').value,'Go');assert.match(h.html(),/第 2 \/ 3 页/);assert.ok(h.calls.every(v=>v.method==='GET'));
});
test('explicit reminder checks report success separately from list failure; list retry never repeats generation',async()=>{
 let failList=false;const h=harness({request:async(path,method)=>{if(method==='POST'){failList=true;return {created:3};}if(failList)throw Error('合成读取失败');return records(3);}});await h.start();await h.elements.get('inbox-check').onclick();assert.match(h.html(),/已检查新提醒，但列表读取失败/);assert.equal(h.articles().length,3);failList=false;await h.elements.get('inbox-retry').onclick();assert.equal(h.calls.filter(v=>v.method==='POST').length,1);assert.doesNotMatch(h.html(),/合成读取失败/);
});
test('initial failures have a local retry and slow reads cannot repaint an abandoned route',async()=>{
 let failed=true;const h=harness({request:async()=>{if(failed)throw Error('合成读取失败');return [];}});await h.start();assert.match(h.html(),/合成读取失败/);assert.doesNotMatch(h.html(),/暂时没有通知/);failed=false;await h.elements.get('inbox-retry').onclick();assert.match(h.html(),/暂时没有通知/);
 let resolve;const slow=harness({request:()=>new Promise(done=>resolve=done)}),opening=slow.start();await new Promise(setImmediate);await slow.elements.get('inbox-refresh').onclick();assert.equal(slow.calls.length,2);const before=slow.html();await slow.leave();resolve(records(100));await opening;assert.equal(slow.html(),before);assert.equal(slow.messages.length,0);
});
test('leaving during an explicit check prevents the subsequent list read and all late UI updates',async()=>{
 let resolve;const h=harness({request:async(path,method)=>method==='POST'?new Promise(done=>resolve=done):records(2)});await h.start();const checking=h.elements.get('inbox-check').onclick(),before=h.html();await h.leave();resolve({created:4});await checking;assert.equal(h.html(),before);assert.equal(h.calls.filter(v=>v.path==='/api/notifications').length,1);assert.equal(h.messages.length,0);
});
test('returning from a job restores account-scoped search, unread view, page, position and origin focus',async()=>{
 const stored=new Map(),rows=records(90),h=harness({stored,rows});await h.start();h.elements.get('inbox-unread').onclick();const input=h.elements.get('inbox-search');input.value='Go';input.oninput({});h.elements.get('inbox-next').onclick();h.scroll(410);const target=h.articles()[3];await target.job.onclick();assert.equal(h.navigations[0].query.returnTo.page,'notifications');assert.equal(h.navigations[0].query.jobID,target.job.dataset.jobId);
 const resumed=harness({stored,rows});await resumed.start({resume:true});assert.equal(resumed.elements.get('inbox-search').value,'Go');assert.equal(resumed.elements.get('inbox-unread')['aria-pressed'],'true');assert.match(resumed.html(),/第 2 \/ 4 页/);assert.equal(resumed.scrolls.at(-1),410);assert.equal(resumed.document.activeElement,resumed.articles().find(a=>a.dataset.notification===target.dataset.notification).job);
 const other=harness({stored,rows,user:'bob'});await other.start({resume:true});assert.equal(other.elements.get('inbox-search').value,'');assert.equal(other.elements.get('inbox-all')['aria-pressed'],'true');assert.match(other.html(),/第 1 \/ 5 页/);
});
test('notification text is escaped while short and empty inboxes keep honest counts',async()=>{
 const h=harness({rows:[{...records(1)[0],title:'<script>alert(1)</script>',body:'<img src=x onerror=alert(2)>'}]});await h.start();assert.doesNotMatch(h.html(),/<script>|<img/);assert.match(h.html(),/&lt;script&gt;/);assert.match(h.html(),/共 1 条/);h.elements.get('inbox-unread').onclick();assert.match(h.html(),/没有未读通知/);assert.match(h.html(),/data-inbox-all/);
});
