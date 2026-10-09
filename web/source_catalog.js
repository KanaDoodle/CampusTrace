'use strict';
(function(root){
 const groups=['互联网','游戏','金融科技','银行','证券','国央企','制造业与消费电子','外企'];
 const ready=s=>s.auto_import!==false;
 const group=s=>String(s.category||'').startsWith('外企')?'外企':['互联网','游戏','金融科技','银行','证券','国央企'].includes(s.category)?s.category:'制造业与消费电子';
 function officialURL(value){try{const u=new URL(value);return u.protocol==='https:'&&!u.username&&!u.password?u.href:'';}catch{return '';}}
 function select(catalog,state={}){
  const query=String(state.search||'').trim().toLocaleLowerCase();
  return catalog.map((site,index)=>({site,index})).filter(({site})=>(!state.group||group(site)===state.group)&&(!query||String(site.company||'').toLocaleLowerCase().includes(query)));
 }
 function results(catalog,esc,state={}){
  const rows=select(catalog,state),automatic=rows.filter(({site})=>ready(site)),manual=rows.filter(({site})=>!ready(site));
  const entries=(items,canImport)=>items.map(({site:s,index})=>`<li class="source-entry"><div class="source-entry-copy"><div class="source-entry-heading"><strong>${esc(s.company)}</strong><span class="source-status ${!canImport||s.status&&s.status!=='已接入'?'source-manual-status':''}">${esc(s.status||(canImport?'已接入':'待适配'))}</span></div><p>${esc(canImport?s.scope:s.note||s.scope)}</p>${canImport&&s.note&&s.status!=='已接入'?`<small>${esc(s.note)}</small>`:''}</div>${canImport?`<button type="button" class="btn btn-small" data-campus-preset="${index}" aria-label="预览${esc(s.company)}校招岗位">选择</button>`:officialURL(s.url)?`<a class="source-official" href="${esc(officialURL(s.url))}" target="_blank" rel="noopener noreferrer">官网查看<span aria-hidden="true"> ↗</span></a>`:'<span class="meta">入口待确认</span>'}</li>`).join('');
  return `<p class="source-count" role="status" aria-live="polite">可自动导入 ${automatic.length} 个 · 官网入口 ${manual.length} 个</p>${automatic.length?`<ul class="source-list">${entries(automatic,true)}</ul>`:'<p class="source-empty">没有符合筛选的自动导入来源。</p>'}${manual.length?`<details class="source-manual" ${state.manualOpen||!automatic.length?'open':''}><summary>其他招聘官网入口（${manual.length} 个）<span>暂不能自动导入</span></summary><p class="meta">以下保留官网入口和接入进展，点击可自行查看招聘信息。核验日期以清单为准。</p><ul class="source-list">${entries(manual,false)}</ul></details>`:''}`;
 }
 function render(catalog,esc,state={}){
  const checked=catalog.map(s=>s.checked_at||'').filter(d=>/^\d{4}-\d{2}-\d{2}$/.test(d)).sort().at(-1);
  return `<section id="source-directory" class="source-directory" aria-label="校招来源目录"><div class="source-directory-heading"><h3>校招来源目录</h3><span>${catalog.length} 个招聘来源</span></div><p class="meta">已支持的校招来源可选择后预览，确认范围与数量再开始导入。${checked?` 核验于 ${esc(checked)}，当前可用性以预览为准。`:""}</p><div class="source-filters"><label>搜索公司<input type="search" data-source-search placeholder="例如：网易、同花顺、天翼云" value="${esc(state.search||'')}"></label><label>来源类型<select data-source-group><option value="">全部类型</option>${groups.map(g=>`<option value="${g}" ${state.group===g?'selected':''}>${g}</option>`).join('')}</select></label></div><div data-source-results>${results(catalog,esc,state)}</div></section>`;
 }
 function mount(section,catalog,{esc,state,bind}){
  if(!section||!section.querySelector)return;
  const search=section.querySelector('[data-source-search]'),type=section.querySelector('[data-source-group]'),list=section.querySelector('[data-source-results]');
  const remember=()=>{const manual=list.querySelector('.source-manual');if(manual)manual.ontoggle=()=>{state.manualOpen=manual.open;};};
  const refresh=()=>{state.search=search.value;state.group=type.value;list.innerHTML=results(catalog,esc,state);bind();remember();};
  search.oninput=refresh;type.onchange=refresh;remember();
 }
 const api={select,render,mount,officialURL,ready,group};root.CampusSources=api;if(typeof module!=='undefined')module.exports=api;
})(typeof globalThis!=='undefined'?globalThis:this);
