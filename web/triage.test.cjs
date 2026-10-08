const {test}=require('node:test'),assert=require('node:assert/strict');
const M=require('./matching.js'),U=require('./ui.js'),D=require('./display.js'),Decision=require('./matching_decision.js');
const helpers={esc:U.esc,D,U};

test('low-coverage application priority and company placement stay visible without a core score',()=>{
 const row={job:{id:'j',title:'后端开发',company:'测试公司',locations:[]},state:'ANALYZED',score:null,coverage:25,priority:{score:62.5,lower:25,upper:100},company_placement:{rank:1,total:3,pending:2,tied:true}};
 const html=M.jobRowHTML(row,helpers,false,false,false,false);
 assert.match(html,/62\.5/);assert.match(html,/投递优先度/);assert.match(html,/公司内技术排序 并列 1 \/ 3/);assert.match(html,/另有 2 个待分析/);assert.doesNotMatch(html,/依据不足/);
 for(const state of ['STALE','BASIC']){const hidden=M.jobRowHTML({...row,state},helpers,false,false,false,false);assert.doesNotMatch(hidden,/62\.5|公司内技术排序/);}
 const result={model:'fixture',score:null,coverage:25,requirements:[],matches:[],qualifications:{status:'UNKNOWN'}};
 const detail=M.overviewHTML({state:'ANALYZED',priority:row.priority,result},row,helpers);assert.match(detail,/投递优先度 62\.5/);assert.match(detail,/参考区间/);
});

test('priority sorting ignores stale scores and does not prioritize excluded jobs',()=>{
 const row=(id,state,score,extra={})=>({job:{id},state,priority:{score},preliminary_score:0,...extra});
 const rows=[row('stale','STALE',100),row('partial','ANALYZED',65),row('closed','ANALYZED',100,{excluded_reason:'closed'}),row('direct','ANALYZED',90)];
 assert.deepEqual(M.orderByPriority(rows).map(r=>r.job.id),['direct','partial','stale','closed']);assert.equal(rows[0].job.id,'stale');
});

test('pure soft requirements are collapsed and do not invent positive matches or ask for proof',()=>{
 const r={requirements:[{id:'soft',category:'REQUIRED',aspect:'SOFT',text:'团队协作',excerpt:'团队协作',confidence:1},{id:'go',category:'REQUIRED',text:'熟悉 Go',excerpt:'Go',confidence:1}],matches:[{requirement_id:'soft',result:'NO_EVIDENCE',explanation:'未记录软性事例',evidence:[]},{requirement_id:'go',result:'NO_EVIDENCE',evidence:[]}],candidate_facts:[]};
 const before=JSON.stringify(r),html=M.renderRequirements(r,helpers,true),traits=html.split('<details class="match-soft">')[1];
 assert.match(traits,/默认不限制投递/);assert.doesNotMatch(traits,/data-supplement|资料待核对|直接匹配/);assert.doesNotMatch(html,/<details class="match-soft"[^>]*open/);assert.match(html,/data-supplement="go"/);assert.equal(JSON.stringify(r),before);
 assert.equal(M.requirementStatus({aspect:'TECHNICAL',confidence:.7},{result:'DIRECT'}),'要求含义待确认');
 assert.equal(M.requirementStatus({aspect:'TECHNICAL',confidence:1},{result:'NO_EVIDENCE',review_note:'INVALID_ABILITY_EVIDENCE'}),'引用待修正');
});

test('soft interview examples remain optional, collapsed, and outside evidence tasks',()=>{
 const plan={job:{id:'j'},state:'ANALYZED',input_key:'k',coverage:0,tasks:[{id:'soft',category:'SOFT',kind:'EVIDENCE',result:'NO_EVIDENCE',title:'热情',action:'面试可准备真实事例',selected_requirement_id:'soft',requirements:[],evidence:[]}]};
 const html=Decision.renderPreparation(plan,helpers);assert.match(html,/可选软性事例 1 项/);assert.match(html,/默认不限制投递/);assert.doesNotMatch(html,/data-supplement|资料待核对/);
});
