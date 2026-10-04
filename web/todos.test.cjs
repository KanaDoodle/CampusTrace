const {test}=require('node:test'),assert=require('node:assert/strict'),Todos=require('./todos.js'),D=require('./display.js');
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
test('todo actions point to exact owned workflow records and viewing failures never starts analysis',()=>{
 assert.deepEqual(Todos.target({kind:'PLANNED_CLOSING',application_id:'a'}),['applications',{applicationID:'a'}]);
 assert.deepEqual(Todos.target({kind:'INTERVIEW_UPCOMING',interview_id:'i'}),['interviews',{interviewID:'i'}]);
 assert.deepEqual(Todos.target({kind:'REVIEW_PENDING',interview_id:'i'}),['review','i']);
 assert.deepEqual(Todos.target({kind:'ANALYSIS_FAILED',task_id:'t'}),['matching',{taskID:'t'}]);
 assert.equal(Todos.target({kind:'unknown'}),null);
});
test('todo view safely distinguishes complete counts from bounded rows and keeps empty/error directions clear',()=>{
 const v={as_of:'2026-10-05T00:00:00Z',counts:{PLANNED_CLOSING:8,INTERVIEW_UPCOMING:1,REVIEW_PENDING:0,ANALYSIS_FAILED:0},truncated:{PLANNED_CLOSING:true},items:[{id:'a',kind:'PLANNED_CLOSING',application_id:'a',job:{company:'<img onerror=x>',title:'<script>岗位',locations:['上海']},at:'2026-10-06T00:00:00Z'}]};
 const html=Todos.render(v,{esc,D});for(const text of ['9 项','&lt;img','&lt;script&gt;岗位','查看投递计划','每类列出前 5 项','查看待办不调用模型'])assert.ok(html.includes(text),text);assert.ok(!html.includes('<img'));assert.ok(!html.includes('<script>'));
 const review=Todos.render(v,{esc,D},'REVIEW_PENDING');assert.ok(review.includes('这类待办已处理完'));assert.ok(!review.includes('data-todo-open'));assert.ok(!review.includes('每类列出'));
});
