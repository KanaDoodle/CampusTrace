const {test}=require('node:test'),assert=require('node:assert/strict'),App=require('./applications.js'),D=require('./display.js');
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
test('application view shows human-readable records, escapes notes and offers only valid stages',()=>{
 const a={id:'a',job_id:'j',job:{company:'测试公司',title:'后端开发<script>',locations:['上海'],job_type:'FULL_TIME'},current_state:'APPLIED',resume_version:'后端版',note:'<img>等待通知',applied_at:'2026-09-29T05:00:00Z',updated_at:'2026-09-29T05:00:00Z',next_states:['OA','INTERVIEW'],official_url:'https://careers.example.invalid/role'};
 const html=App.render([a],{esc,D});for(const v of ['测试公司','后端开发&lt;script&gt;','后端版','投递时间','等待通知','查看招聘官网','noopener noreferrer','本次进展说明'])assert.ok(html.includes(v),v);
 assert.ok(!html.includes('<img>'));assert.ok(!html.includes('<script>'));assert.ok(html.includes('value="OA"'));assert.ok(!html.includes('value="PLANNED"'));
 const old=App.render([{id:'old',job_id:'j',current_state:'OFFER',next_states:[]}],{esc,D});assert.ok(old.includes('公司信息暂不可用'));assert.ok(!old.includes('id="application-stage-'));assert.ok(old.includes('保存投递资料'));
});
test('official links reject executable schemes, credential URLs and malformed values',()=>{
 for(const u of ['javascript:alert(1)','data:text/html,hi','https://secret:pass@example.invalid/','not-url',undefined])assert.equal(App.officialLink(u),'');
 assert.equal(App.officialLink('https://careers.example.invalid/'),'https://careers.example.invalid/');
});
test('application filters combine company, title and stage without treating offer as ongoing',()=>{
 const rows=[{id:'a',job:{company:'甲公司',title:'Go 服务端开发'},current_state:'APPLIED'},{id:'b',job:{company:'甲公司',title:'Go 平台开发'},current_state:'OFFER'},{id:'c',job:{company:'乙公司',title:'Go 服务端开发'},current_state:'REJECTED'},{id:'d',current_state:'WITHDRAWN'}];
 assert.deepEqual(App.filtered(rows,{query:' go ',company:'甲公司',stage:'APPLIED'}).map(a=>a.id),['a']);assert.deepEqual(rows.filter(App.ended).map(a=>a.id),['b','c','d']);assert.equal(App.filtered(rows,{query:'不存在'}).length,0);assert.equal(App.filtered(rows).length,4);
 const many=Array.from({length:51},(_,i)=>({id:String(i)}));assert.equal(App.pageSlice(many,2).rows[0].id,'25');assert.equal(App.pageSlice(many,100).page,3);assert.equal(App.pageSlice(many,3).rows.length,1);
});
test('follow-up times use Beijing wall time and reject normalized invalid dates',()=>{
 assert.equal(App.followUpTime('2026-10-11T10:00'),'2026-10-11T02:00:00.000Z');
 assert.equal(App.followUpInput('2026-10-11T02:00:00Z'),'2026-10-11T10:00');
 assert.equal(App.followUpTime('2000-01-01T00:00'),'1999-12-31T16:00:00.000Z');
 assert.equal(App.followUpTime('2100-12-31T23:59'),'2100-12-31T15:59:00.000Z');
 for(const value of ['2026-02-30T10:00','2026-10-11T24:00','1999-12-31T23:59','2101-01-01T00:00','2026-10-11',null])assert.equal(App.followUpTime(value),null,value);
 assert.equal(App.followUpShortcut(1,Date.parse('2026-12-31T18:00:00Z')),'2027-01-02T10:00');
});
test('follow-up filtering uses the Beijing day, searches actions and sorts without changing the original list',()=>{
 const now=Date.parse('2026-10-10T02:00:00Z'),rows=[
  {id:'future',current_state:'APPLIED',follow_up_at:'2026-10-10T16:00:00Z',updated_at:'2026-10-09T00:00:00Z'},
  {id:'today',current_state:'INTERVIEW',follow_up_at:'2026-10-10T15:59:59Z',next_action:'查看笔试通知',applied_at:'2026-10-07T00:00:00Z'},
  {id:'past',current_state:'APPLIED',follow_up_at:'2026-10-08T02:00:00Z',applied_at:'2026-10-09T00:00:00Z'},
  {id:'ended',current_state:'REJECTED',follow_up_at:'2026-10-07T00:00:00Z',updated_at:'2026-10-10T00:00:00Z'},
  {id:'none',current_state:'PLANNED',updated_at:'2026-10-08T00:00:00Z'}];
 const original=rows.map(r=>r.id);
 assert.deepEqual(App.filtered(rows,{followUpOnly:true,sort:'followup',now}).map(r=>r.id),['past','today']);
 assert.deepEqual(App.filtered(rows,{query:'笔试'}).map(r=>r.id),['today']);
 assert.deepEqual(App.filtered(rows,{sort:'followup'}).map(r=>r.id),['past','today','future','ended','none']);
 assert.deepEqual(App.filtered(rows,{sort:'applied'}).map(r=>r.id),['past','today','ended','future','none']);
 assert.deepEqual(rows.map(r=>r.id),original);
});
test('reminders display escaped next actions and only active applications offer editing or completion',()=>{
 const a={id:'a',current_state:'APPLIED',follow_up_at:'2026-10-10T02:00:00Z',next_action:'<img>跟进',next_states:[]};
 const html=App.render([a],{esc,D,now:Date.parse('2026-10-10T03:00:00Z')});
 for(const text of ['已到跟进时间','&lt;img&gt;跟进','完成跟进','北京时间','value="2026-10-10T10:00"','取消提醒','data-follow-up-days="7"'])assert.ok(html.includes(text),text);
 assert.ok(!html.includes('<img>'));const closed=App.render([{...a,current_state:'OFFER'}],{esc,D});
 assert.ok(!closed.includes('完成跟进'));assert.ok(!closed.includes('设置跟进提醒'));assert.ok(!closed.includes('取消提醒'));
});
