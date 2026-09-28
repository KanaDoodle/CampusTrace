const {test}=require('node:test');
const assert=require('node:assert/strict');
const m=require('./matching.js');
const D=require('./display.js');
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const row=(id,state='BASIC',bytes=4000,excluded='')=>({job:{id,company:'小红书',title:'后端开发'},state,text_bytes:bytes,excluded_reason:excluded});
test('all inventory can be screened but only the first 30 eligible jobs enter deep analysis',()=>{
 const rows=Array.from({length:1000},(_,i)=>row(String(i)));
 rows[0].state='ANALYZED';rows[1].excluded_reason='明确不符合';rows[2].state='STALE';
 const selected=m.shortlist(rows,30);assert.equal(selected.length,30);assert.equal(selected[0].job.id,'2');assert.equal(selected.at(-1).job.id,'31');
});
test('batching respects both job count and text budget, and failed-item retries can be individual',()=>{
 assert.equal(m.pack([row('a'),row('b'),row('c'),row('d')]).length,3);
 assert.equal(m.pack([row('a','BASIC',18000),row('b','BASIC',10000)]).length,1);
 assert.equal(m.pack([row('a'),row('b')],1).length,1);
 assert.equal(m.pack([row('a','BASIC',25000)]).length,0);
});
test('company and analysis-state filters remain separate from scores',()=>{
 assert.equal(m.filtered([row('a'),row('b','STALE')],'小红书','STALE')[0].job.id,'b');
 assert.equal(m.filtered([row('a')],'另一公司','').length,0);
});
test('matching output is escaped and low coverage or stale results do not show a reliable score',()=>{
 const result={state:'ANALYZED',result:{score:null,coverage:25,model:'endpoint\nmodel',analyzed_at:'2026-09-28T00:00:00Z',requirements:[{id:'r',category:'REQUIRED',text:'<script>bad</script>',excerpt:'原文',confidence:1}],matches:[{requirement_id:'r',result:'NO_EVIDENCE',explanation:'资料不足',evidence:[]}],candidate_facts:[],qualifications:{results:[]}}};
 const html=m.renderResult(result,{esc,D});assert.ok(html.includes('暂无法可靠评分'));assert.ok(html.includes('暂无依据'));assert.ok(html.includes('&lt;script&gt;'));assert.ok(!html.includes('<script>'));
 result.state='STALE';assert.ok(!m.renderResult(result,{esc,D}).includes('25.0%'));
});
test('resume progress is account, candidate and model scoped and carries no credential',()=>{
 const saved=new Map();global.sessionStorage={getItem:k=>saved.get(k)??null};
 saved.set('campustrace:match-progress:v1:alice',JSON.stringify({hash:'profile1',model:'model1',pending:['a'],failed:[]}));
 assert.deepEqual(m.readProgress('alice','profile1','model1').pending,['a']);
 assert.deepEqual(m.readProgress('bob','profile1','model1').pending,[]);
 assert.deepEqual(m.readProgress('alice','profile2','model1').pending,[]);
 assert.deepEqual(m.readProgress('alice','profile1','model2').pending,[]);
});
test('body-derived direction and local tiers participate in filtering without affecting analysis state',()=>{
 const a=row('a');a.job.title='研发工程师';a.local={role:'后端开发',tier:'HIGH'};
 const b=row('b','STALE');b.local={role:'基础架构与平台',tier:'POSSIBLE'};
 assert.deepEqual(m.filtered([a,b],'服务端','BASIC','HIGH').map(j=>j.job.id),['a']);
 assert.deepEqual(m.filtered([a,b],'','STALE','POSSIBLE').map(j=>j.job.id),['b']);
 assert.equal(m.filtered([row('c')],'','','UNCERTAIN').length,1);
});
test('local reasons and evidence are escaped, separate mandatory/bonus/alternative clues, and missing evidence is not inability',()=>{
 const a=row('a');a.local={score:57.5,tier:'POSSIBLE',role:'后端开发',role_source:'BODY',role_excerpt:'开发服务接口',reasons:['<script>bad</script>'],warnings:['必需项待核对'],checks:[{category:'REQUIRED',mode:'ANY',terms:['Go','Java'],excerpt:'Go / Java',result:'SIGNAL',evidence:[{kind:'IMPLEMENTED',excerpt:'实现 <Go> 服务'}]},{category:'BONUS',mode:'ALL',terms:['Redis'],excerpt:'Redis 优先',result:'NO_EVIDENCE',evidence:[]}]};
 const html=m.renderLocal(a,{esc});
 for(const word of ['可能相关','来自岗位职责','必需能力','加分项','任选一项','逐项核对','已确认项目事实','资料中暂无依据','57.5','更新时间仅用于同分排序'])assert.ok(html.includes(word),word);
 assert.ok(!html.includes('<script>'));assert.ok(html.includes('&lt;Go&gt;'));assert.ok(!html.includes('你不具备'));
 assert.ok(m.renderLocal(row('missing'),{esc}).includes('岗位原文尚不可用'));
});
test('core score, separate sections, selectable directions and project context remain visible and escaped',()=>{
 const result={state:'ANALYZED',result:{score:75,coverage:100,model:'endpoint\nmodel',analyzed_at:'2026-09-28T00:00:00Z',requirements:[{id:'r',category:'RESPONSIBILITY',text:'建设数据安全平台',excerpt:'数据安全平台',confidence:1,group_id:'directions',group_excerpt:'参与一个或多个方向'},{id:'soft',category:'REQUIRED',aspect:'SOFT',text:'沟通顺畅',excerpt:'沟通顺畅',confidence:1}],matches:[{requirement_id:'r',result:'TRANSFERABLE',explanation:'相关机制可迁移',evidence:[{id:'f',excerpt:'实现重试'}]}],candidate_facts:[{id:'f',kind:'IMPLEMENTED',text:'实现重试',project_name:'<任务平台>'}],qualifications:{status:'UNKNOWN',results:[]},breakdown:[{category:'REQUIRED',total:2,known:2,direct:1,partial:1,transferable:0,missing:0,mismatch:0,coverage:100},{category:'RESPONSIBILITY',total:1,known:1,coverage:100},{category:'BONUS',total:3,known:0,coverage:0},{category:'SOFT',total:1,known:0,coverage:0}]}};
 const html=m.renderResult(result,{esc,D});
 for(const word of ['核心技术匹配度','分析摘要','直接匹配 1 项','工作内容相关性','加分项','软性要求','同组任选方向','&lt;任务平台&gt;','资格暂无法判断'])assert.ok(html.includes(word),word);
 assert.ok(!html.includes('<任务平台>'));
 result.state='STALE';const stale=m.renderResult(result,{esc,D});
 assert.ok(stale.includes('查看上次分析依据（已过期）'));assert.ok(stale.includes('数据安全平台'));assert.ok(!stale.includes('75.0 / 100'));assert.ok(!stale.includes('100.0%'));
});


test('withdrawn capability evidence remains explicit and cannot display its former proof',()=>{
 const v={state:'ANALYZED',result:{score:null,coverage:50,model:'fixture',analyzed_at:'2026-09-28T00:00:00Z',requirements:[{id:'r',category:'REQUIRED',text:'具体能力',excerpt:'要求原文',confidence:1}],matches:[{requirement_id:'r',result:'NO_EVIDENCE',review_note:'INVALID_ABILITY_EVIDENCE',explanation:'原结论已撤销，不代表你不会。',evidence:[]}],candidate_facts:[{id:'city',kind:'CITY_PREFERRED',text:'误用的城市事实'}],qualifications:{status:'UNKNOWN',results:[]}}};
 const html=m.renderResult(v,{esc,D});
 for(const word of ['1 项结论因误用偏好或项目局限已撤销','错误引用已撤销','暂无依据','暂无法可靠评分','不代表你不会'])assert.ok(html.includes(word),word);
 assert.ok(!html.includes('误用的城市事实'));assert.ok(!html.includes('100.0 / 100'));
});
