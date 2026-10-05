const test=require('node:test'),assert=require('node:assert/strict'),S=require('./source_catalog.js');
const esc=s=>String(s??'').replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;');
const catalog=[{company:'百度',checked_at:'2026-10-05',category:'互联网',auto_import:true,scope:'应届生',url:'https://talent.baidu.com/'},{company:'SAP',category:'外企与软件',auto_import:false,status:'待适配',note:'需核验中国毕业生项目',url:'https://jobs.sap.com/'},{company:'美的集团',category:'制造业',auto_import:true,scope:'2027届美的星',url:'https://careers.midea.com/'}];
test('搜索公司和行业筛选保留原始索引，未接入公司没有自动导入按钮',()=>{
 assert.deepEqual(S.select(catalog,{search:' 美的 ',group:'制造业与消费电子'}).map(v=>v.index),[2]);
 assert.deepEqual(S.select(catalog,{search:'sap',group:'外企'}).map(v=>v.index),[1]);
 const h=S.render(catalog,esc);assert.match(h,/可自动导入 2 家 · 官网入口 1 家/);assert.match(h,/核验于 2026-10-05/);assert.match(h,/data-campus-preset="2"/);assert.doesNotMatch(h,/data-campus-preset="1"/);assert.match(h,/需核验中国毕业生项目/);assert.match(h,/rel="noopener noreferrer"/);
 assert.match(S.render(catalog,esc,{search:'sap'}),/<details class="source-manual" open>/);assert.match(S.render(catalog,esc,{search:'不存在'}),/没有符合筛选/);
});
test('目录转义所有官网文本并拒绝危险链接',()=>{
 const h=S.render([{company:'<script>',scope:'<img>',auto_import:false,note:'"<iframe>',status:'<svg>',url:'javascript:alert(1)'}],esc);
 assert.doesNotMatch(h,/<script>|<iframe>|<svg>|javascript:/);assert.match(h,/&lt;script&gt;/);assert.match(h,/入口待确认/);
 for(const u of ['http://site.test','https://user:secret@site.test','invalid'])assert.equal(S.officialURL(u),'');
});
test('搜索不重绘其他表单，展开状态在重新筛选后保留',()=>{
 let rebound=0;const search={value:''},type={value:''},manual={open:true};
 const list={innerHTML:'',querySelector:()=>manual},section={querySelector:s=>s==='[data-source-search]'?search:s==='[data-source-group]'?type:list};
 const state={search:'',group:'',manualOpen:false};S.mount(section,catalog,{esc,state,bind:()=>{rebound++;}});manual.ontoggle();search.value='SAP';search.oninput();assert.equal(state.manualOpen,true);assert.equal(state.search,'SAP');assert.equal(rebound,1);assert.match(list.innerHTML,/官网查看/);type.value='互联网';type.onchange();assert.match(list.innerHTML,/没有符合筛选/);
});
