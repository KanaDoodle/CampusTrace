const test=require('node:test'),assert=require('node:assert/strict'),S=require('./source_catalog.js');
const esc=s=>String(s??'').replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;');
const catalog=[{company:'百度',checked_at:'2026-10-05',category:'互联网',auto_import:true,scope:'应届生',url:'https://talent.baidu.com/'},{company:'SAP',category:'外企与软件',auto_import:false,status:'待适配',note:'需核验中国毕业生项目',url:'https://jobs.sap.com/'},{company:'美的集团',category:'制造业',auto_import:true,scope:'2027届美的星',url:'https://careers.midea.com/'}];
test('搜索公司和行业筛选保留原始索引，未接入公司没有自动导入按钮',()=>{
 assert.deepEqual(S.select(catalog,{search:' 美的 ',group:'制造业与消费电子'}).map(v=>v.index),[2]);
 assert.deepEqual(S.select(catalog,{search:'sap',group:'外企'}).map(v=>v.index),[1]);
 const h=S.render(catalog,esc);assert.match(h,/可自动导入 2 个 · 官网入口 1 个/);assert.match(h,/核验于 2026-10-05/);assert.match(h,/data-campus-preset="2"/);assert.doesNotMatch(h,/data-campus-preset="1"/);assert.match(h,/需核验中国毕业生项目/);assert.match(h,/rel="noopener noreferrer"/);
 assert.match(S.render(catalog,esc,{search:'sap'}),/<details class="source-manual" open>/);assert.match(S.render(catalog,esc,{search:'不存在'}),/没有符合筛选/);
});
test('目录转义所有官网文本并拒绝危险链接',()=>{
 const h=S.render([{company:'<script>',scope:'<img>',auto_import:false,note:'"<iframe>',status:'<svg>',url:'javascript:alert(1)'}],esc);
 assert.doesNotMatch(h,/<script>|<iframe>|<svg>|javascript:/);assert.match(h,/&lt;script&gt;/);assert.match(h,/入口待确认/);
 for(const u of ['http://site.test','https://user:secret@site.test','invalid'])assert.equal(S.officialURL(u),'');
});
test('游戏金融科技和国央企可独立筛选，电信集团入口不会变成自动导入',()=>{
 const rows=[{company:'网易游戏雷火',category:'游戏',auto_import:true,scope:'2027届'}, {company:'同花顺',category:'金融科技',auto_import:true,scope:'2027届（含实习转正）'}, {company:'天翼云科技有限公司',category:'国央企',auto_import:true,scope:'2027秋招'}, {company:'中国电信（集团入口）',category:'国央企',auto_import:false,url:'https://job.chinatelecom.com.cn/',note:'需按单位接入'}];
 for(const [group,indices] of [['游戏',[0]],['金融科技',[1]],['国央企',[2,3]]]) assert.deepEqual(S.select(rows,{group}).map(v=>v.index),indices);
 const h=S.render(rows,esc,{group:'国央企'});assert.match(h,/4 个招聘来源/);assert.match(h,/可自动导入 1 个 · 官网入口 1 个/);assert.match(h,/data-campus-preset="2"/);assert.doesNotMatch(h,/data-campus-preset="3"/);
 for(const g of ['游戏','金融科技','国央企'])assert.ok(h.includes(`<option value="${g}"`));
});
test('搜索不重绘其他表单，展开状态在重新筛选后保留',()=>{
 let rebound=0;const search={value:''},type={value:''},manual={open:true};
 const list={innerHTML:'',querySelector:()=>manual},section={querySelector:s=>s==='[data-source-search]'?search:s==='[data-source-group]'?type:list};
 const state={search:'',group:'',manualOpen:false};S.mount(section,catalog,{esc,state,bind:()=>{rebound++;}});manual.ontoggle();search.value='SAP';search.oninput();assert.equal(state.manualOpen,true);assert.equal(state.search,'SAP');assert.equal(rebound,1);assert.match(list.innerHTML,/官网查看/);type.value='互联网';type.onchange();assert.match(list.innerHTML,/没有符合筛选/);
});
test('银行独立筛选保留技术中心预设和四大行待接入状态',()=>{
 const rows=[{company:'中行 · 软件中心',category:'银行',auto_import:true,scope:'2027届'}, {company:'工商银行',category:'银行',auto_import:false,url:'https://job.icbc.com.cn/',status:'公开查询返回异常'}, {company:'招银网络科技',category:'金融科技',auto_import:true}];
 assert.deepEqual(S.select(rows,{group:'银行'}).map(v=>v.index),[0,1]);
 const h=S.render(rows,esc,{group:'银行'});assert.match(h,/<option value="银行" selected/);assert.match(h,/可自动导入 1 个 · 官网入口 1 个/);assert.match(h,/data-campus-preset="0"/);assert.doesNotMatch(h,/data-campus-preset="1"/);assert.match(h,/公开查询返回异常/);
});
test('证券独立筛选保留已接通预设和待适配官网，不混入银行金融科技',()=>{
 const rows=[{company:'中金公司',category:'证券',auto_import:true}, {company:'中信证券',category:'证券',auto_import:false,url:'https://careers.citics.com/',status:'公开协议待适配'}, {company:'中信银行',category:'银行',auto_import:true}, {company:'恒生电子',category:'金融科技',auto_import:true}];
 assert.deepEqual(S.select(rows,{group:'证券'}).map(v=>v.index),[0,1]);
 const h=S.render(rows,esc,{group:'证券'});assert.match(h,/<option value="证券" selected/);assert.match(h,/可自动导入 1 个 · 官网入口 1 个/);assert.match(h,/data-campus-preset="0"/);assert.doesNotMatch(h,/data-campus-preset="1"/);assert.match(h,/公开协议待适配/);
});
