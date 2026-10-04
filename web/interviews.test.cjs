const {test}=require('node:test'),assert=require('node:assert/strict'),Interviews=require('./interviews.js'),D=require('./display.js');
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const fixture=(id,extra={})=>({id,application_id:'a'+id,round:1,scheduled_at:'2026-10-05T08:00:00Z',result:'PENDING',job:{id:'j'+id,company:'测试公司',title:'Go 后端开发',locations:['上海']},...extra});
test('completed-but-awaiting-feedback belongs to review work, not upcoming interviews; a review alone does not mark completion',()=>{
 const rows=[fixture('future'),fixture('reviewed',{review:{id:'r'}}),fixture('awaiting',{finished_at:'2026-10-04T08:00:00Z'}),fixture('done',{result:'PASS',finished_at:'2026-10-03T08:00:00Z',review:{id:'r2'}})];
 assert.deepEqual(Interviews.filtered(rows,{view:'pending'}).map(v=>v.id),['future','reviewed']);assert.deepEqual(Interviews.filtered(rows,{view:'review'}).map(v=>v.id),['awaiting']);assert.deepEqual(Interviews.filtered(rows,{view:'completed'}).map(v=>v.id),['awaiting','done']);
 assert.equal(Interviews.filtered(rows,{query:'Go',company:'测试公司'}).length,4);assert.equal(Interviews.filtered(rows,{company:'其他公司'}).length,0);assert.equal(Interviews.completed({finished_at:'0001-01-01T00:00:00Z'}),false);
});
test('interview view gives company/job context and safely renders original notes and review with explicit completion choices',()=>{
 const html=Interviews.render([fixture('1',{notes:'<script>本轮备注',finished_at:'2026-10-04T08:00:00Z',review:{actual_questions:['<img>事务如何隔离？'],self_evaluation:'<script>复盘',missed_points:['行锁'],follow_up_notes:'复习隔离级别'}})],{esc,D});
 for(const value of ['测试公司','Go 后端开发','已记录完成','已填写','更新本轮反馈','value="PENDING"','已完成，等待反馈','查看投递进展','岗位准备清单','首次记录完成','&lt;img&gt;','事务如何隔离'])assert.ok(html.includes(value),value);
 assert.ok(!html.includes('<script>'));assert.ok(!html.includes('<img>'));assert.ok(!html.includes('填写本轮复盘'));
 const upcoming=Interviews.render([fixture('2')],{esc,D,now:Date.parse('2026-10-04T00:00:00Z')});assert.ok(upcoming.includes('待面试'));assert.ok(!upcoming.includes('首次记录完成'));
});
