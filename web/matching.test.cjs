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
