const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const {esc}=require('./ui.js'),D=require('./display.js'),Deadlines=require('./deadlines.js');
const item=(id,hours=6)=>({job:{id,company:'虚构科技',title:'Go 服务端'},at:new Date(Date.now()+hours*3600000).toISOString(),planned:false,saved:false});
const packet=()=>({as_of:new Date().toISOString(),total:2,within_day:1,within_3_days:2,items:[item('a'),item('b',48)]});
test('deadline panel shows window counts, date-only wording and escaped job metadata',()=>{
 const snapshot=packet();snapshot.items[0].job.company='<script>danger</script>';snapshot.items[0].job.title='Go "后端"';snapshot.items[0].date='2026-12-01';snapshot.items[0].planned=true;
 const html=Deadlines.render({snapshot},{esc,D});assert.match(html,/24 小时内 <strong>1/);assert.match(html,/7 天内 <strong>2/);assert.match(html,/&lt;script&gt;/);assert.doesNotMatch(html,/<script>/);assert.match(html,/2026-12-01 当天结束/);assert.match(html,/已计划/);assert.match(html,/查看全部/);assert.match(html,/data-deadline-job="a"/);
});
test('loading, read failure and confirmed empty state remain distinct',()=>{
 assert.match(Deadlines.render({}, {esc,D}),/正在读取/);const error=Deadlines.render({error:'读取失败'},{esc,D});assert.match(error,/读取失败/);assert.doesNotMatch(error,/暂无/);assert.match(Deadlines.render({snapshot:{items:[],total:0}},{esc,D}),/未来 7 天暂无已确认/);
 const expired=packet();expired.items[0].at=new Date(Date.now()-1000).toISOString();assert.doesNotMatch(Deadlines.render({snapshot:expired},{esc,D}),/data-deadline-job="a"/);
});
function fixture(request){
 const timers=[],calls=[],navigations=[],listeners=new Map();let changes=0,active=true,now=Date.now();
 const c={Date:class extends Date{static now(){return now;}},document:{visibilityState:'visible',addEventListener:(type,fn)=>listeners.set(type,fn),removeEventListener:type=>listeners.delete(type)},setTimeout:(fn,delay)=>(timers.push({fn,delay}),timers.length),clearTimeout:id=>{if(timers[id-1])timers[id-1].canceled=true;}};vm.createContext(c);vm.runInContext(fs.readFileSync(__dirname+'/deadlines.js','utf8'),c);
 const panel=c.CampusDeadlines.create({api:async path=>{calls.push(path);return request();},esc,D,navigate:(...args)=>navigations.push(args),active:()=>active,onChange:()=>changes++});
 const controls=new Map(['[data-deadline-refresh]','[data-deadline-all]','job'].map(k=>[k,{dataset:{deadlineJob:'a'}}]));const host={querySelector:s=>controls.get(s),querySelectorAll:()=>[controls.get('job')]};
 return {panel,calls,timers,navigations,controls,bind:()=>panel.bind(host),deactivate:()=>active=false,hide:()=>{c.document.visibilityState='hidden';return listeners.get('visibilitychange')?.();},show:()=>{c.document.visibilityState='visible';return listeners.get('visibilitychange')?.();},advance:ms=>now+=ms,listeners,changes:()=>changes};
}
test('deadline loading is deduplicated, read-only, navigable and preserves the prior view on retryable failure',async()=>{
 let resolve,fail=false;const h=fixture(()=>fail?Promise.reject(Error('合成失败')):new Promise(done=>resolve=done));const first=h.panel.start(),again=h.panel.load();await again;assert.equal(h.calls.length,1);resolve(packet());await first;assert.equal(h.timers[0].delay,60000);
 h.bind();h.controls.get('job').onclick();h.controls.get('[data-deadline-all]').onclick();assert.equal(h.navigations[0][0],'matching');assert.equal(h.navigations[0][1].jobID,'a');assert.deepEqual(h.navigations[1],['closing',7]);
 fail=true;await h.panel.load();assert.match(h.panel.html(),/合成失败/);assert.match(h.panel.html(),/保留上次/);assert.match(h.panel.html(),/data-deadline-job="a"/);assert.ok(h.calls.every(path=>path==='/api/radar/deadlines'));h.panel.dispose();assert.equal(h.timers[0].canceled,true);
});
test('a late response after leaving cannot update another page or schedule further refreshes',async()=>{
 let resolve;const h=fixture(()=>new Promise(done=>resolve=done)),opening=h.panel.start();const before=h.changes();h.deactivate();resolve(packet());await opening;assert.equal(h.changes(),before);assert.equal(h.timers.length,0);assert.doesNotMatch(h.panel.html(),/data-deadline-job/);
});
test('a hidden page does not request deadline data and a disposed controller stops polling',async()=>{
 const h=fixture(packet);h.hide();await h.panel.start();assert.equal(h.calls.length,0);assert.equal(h.timers.length,0);assert.equal(h.listeners.size,1);h.panel.dispose();await h.show();assert.equal(h.calls.length,0);assert.equal(h.listeners.size,0);
});

test('returning to a stale tab refreshes immediately, while a fresh tab reuses the remaining interval',async()=>{
 const h=fixture(packet);await h.panel.start();await h.panel.start();assert.equal(h.calls.length,1,'starting twice cannot add another request or timer');
 await h.hide();assert.equal(h.timers[0].canceled,true);h.advance(10000);await h.show();assert.equal(h.calls.length,1);assert.equal(h.timers.at(-1).delay,50000);
 await h.hide();h.advance(51000);await h.show();assert.equal(h.calls.length,2);assert.equal(h.timers.at(-1).delay,60000);h.panel.dispose();assert.equal(h.listeners.size,0);
});
test('a request that finishes in a hidden tab cannot restart a hidden polling loop',async()=>{
 let resolve;const h=fixture(()=>new Promise(done=>resolve=done)),opening=h.panel.start();await h.hide();resolve(packet());await opening;assert.equal(h.timers.length,0);await h.show();assert.equal(h.calls.length,1);assert.equal(h.timers.length,1);h.panel.dispose();
});
