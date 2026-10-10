const {test}=require('node:test'),assert=require('node:assert/strict');
const A=require('./agent_actions.js'),{esc}=require('./ui.js');
const job='a'.repeat(32),doc='b'.repeat(32);
test('next steps open only known read-only destinations, never actions or paid analysis',()=>{
 for(const v of [{kind:'confirm',action_id:job},{kind:'analyze',job_id:job},{kind:'url',url:'https://evil.example'},{kind:'job',job_id:'javascript:alert(1)'},{kind:'company',company:'x\nheader'},{kind:'knowledge_source',document_id:'../private'}])assert.equal(A.destination(v),null);
 assert.deepEqual(A.destination({kind:'preparation',job_id:job,analyze:true}),{page:'matching',query:{jobID:job,view:'preparation'}});
 assert.deepEqual(A.destination({kind:'review_topic',topic:'Redis'}),{page:'agent',query:{skill:{id:'review-plan',value:'Redis'}}});
 assert.deepEqual(A.destination({kind:'knowledge_source',document_id:doc}),{page:'knowledge',query:{documentID:doc}});
 assert.deepEqual(A.destination({kind:'matching_tasks',run_id:job}),{page:'matching',query:{taskID:job}});
});
test('action text is escaped and invalid hints do not shift button targets',()=>{
 const rows=[{kind:'url',label:'bad',reason:'bad'},{kind:'job',job_id:job,label:'<script>job</script>',reason:'<img onerror=x>'}];
 const html=A.markup(rows,esc);assert.doesNotMatch(html,/<script>|<img/);assert.match(html,/data-agent-next="0"/);assert.match(html,/aria-label="根据本次记录继续"/);
 const button={dataset:{agentNext:'0'}},seen=[];
 A.bind({querySelectorAll:()=>[button]},rows,{navigate:(page,query)=>seen.push({page,query}),fail:assert.fail});button.onclick();
 assert.deepEqual(seen,[{page:'matching',query:{jobID:job}}]);
});
test('restarting an invalidated task copies its scope but never its old replay key',()=>{
 const v={state:'STALE',skill_id:'interview-prep',input:{skill_id:'interview-prep',job_id:job,request_key:'old-key'}};
 assert.deepEqual(A.restartInput(v),{id:'interview-prep',value:job});
 assert.equal(A.restartInput({...v,input:null}),null);assert.equal(A.restartInput({...v,state:'RUNNING'}),null);
 assert.equal(A.restartInput({...v,input:{...v.input,skill_id:'company-choice'}}),null);
});
