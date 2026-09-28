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
