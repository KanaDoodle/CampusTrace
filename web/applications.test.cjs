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
