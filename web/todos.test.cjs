const {test}=require('node:test'),assert=require('node:assert/strict'),Todos=require('./todos.js'),D=require('./display.js');
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
test('upcoming interview preparation points to its application snapshot, never a new job analysis',()=>{
 const application='a'.repeat(32),item={id:'upcoming',kind:'INTERVIEW_UPCOMING',application_id:application,interview_id:'b'.repeat(32),at:'2099-01-01T00:00:00Z'};
 const html=Todos.render({items:[item]},{esc,D});assert.ok(html.includes('data-todo-prepare="'+application+'"'));assert.ok(html.includes('按留档准备'));assert.ok(!html.includes('data-analyze'));
 assert.ok(!Todos.render({items:[{...item,application_id:'bad-id'}]},{esc,D}).includes('data-todo-prepare'));
});
test('todo actions point to exact owned workflow records and viewing failures never starts analysis',()=>{
 assert.deepEqual(Todos.target({kind:'PLANNED_CLOSING',application_id:'a'}),['applications',{applicationID:'a'}]);
 assert.deepEqual(Todos.target({kind:'APPLICATION_FOLLOW_UP',application_id:'a'}),['applications',{applicationID:'a'}]);
 assert.deepEqual(Todos.target({kind:'INTERVIEW_UPCOMING',interview_id:'i'}),['interviews',{interviewID:'i'}]);
 assert.deepEqual(Todos.target({kind:'REVIEW_PENDING',interview_id:'i'}),['review','i']);
 assert.deepEqual(Todos.target({kind:'ANALYSIS_FAILED',task_id:'t'}),['matching',{taskID:'t'}]);
 assert.equal(Todos.target({kind:'unknown'}),null);
});
test('today follow-ups display the intended action without exposing application notes',()=>{
 const html=Todos.render({counts:{APPLICATION_FOLLOW_UP:1},items:[{id:'follow-up:a',application_id:'a',kind:'APPLICATION_FOLLOW_UP',next_action:'<script>完成网申',note:'PRIVATE',at:'2026-10-10T02:00:00Z'}]},{esc,D});
 for(const value of ['投递跟进','处理跟进','跟进时间','&lt;script&gt;完成网申','1 项'])assert.ok(html.includes(value),value);
 assert.ok(!html.includes('<script>'));assert.ok(!html.includes('PRIVATE'));assert.ok(!html.includes('不调用模型'));
});
test('todo view safely distinguishes complete counts from bounded rows and keeps empty/error directions clear',()=>{
 const v={as_of:'2026-10-05T00:00:00Z',counts:{PLANNED_CLOSING:8,INTERVIEW_UPCOMING:1,REVIEW_PENDING:0,ANALYSIS_FAILED:0},truncated:{PLANNED_CLOSING:true},items:[{id:'a',kind:'PLANNED_CLOSING',application_id:'a',job:{company:'<img onerror=x>',title:'<script>岗位',locations:['上海']},at:'2026-10-06T00:00:00Z'}]};
 const html=Todos.render(v,{esc,D});for(const text of ['9 项','&lt;img','&lt;script&gt;岗位','查看投递计划','每类列出前 5 项','更新于'])assert.ok(html.includes(text),text);assert.ok(!html.includes('<img'));assert.ok(!html.includes('<script>'));
 const review=Todos.render(v,{esc,D},'REVIEW_PENDING');assert.ok(review.includes('这类待办已处理完'));assert.ok(!review.includes('data-todo-open'));assert.ok(!review.includes('每类列出'));
});
