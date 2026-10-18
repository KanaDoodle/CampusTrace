'use strict';
(function(root){
 const failures=new Set(['FAILED','PARTIAL_FAILURE','DISCOVERY_FAILED','RESTORED_PAUSED']);
 const outcomes={RESTORED_PAUSED:'恢复后已暂停',QUEUED:'等待后台检查',PENDING:'等待检查',PROCESSING:'正在导入',SUCCESS:'检查完成',PARTIAL_FAILURE:'部分岗位获取失败',DISCOVERY_FAILED:'来源暂时无法读取',FAILED:'本次检查失败'};
 const schedules={FIXED:'固定间隔',BASE:'基础间隔',PRIORITY:'重点关注或临近截止',RECENT_CHANGE:'近期有变化',UNCHANGED_BACKOFF:'近期无变化，已放缓',FAILURE_BACKOFF:'连接失败，稍后重试'};
 const attention=w=>failures.has(w.last_outcome);
 function matches(w,source,state={}){
  const query=String(state.search||'').trim().toLocaleLowerCase();
  return (!query||String(source?.name||w.source_id).toLocaleLowerCase().includes(query))&&(!state.status||state.status==='ALL'||state.status==='ENABLED'&&w.enabled||state.status==='PAUSED'&&!w.enabled||state.status==='ATTENTION'&&attention(w)||state.status==='PRIORITY'&&w.priority);
 }
 function minimum(source,catalog=[]){
  const seconds=Number(source?.minimum_interval||catalog.find(v=>v.adapter===source?.adapter)?.minimum_interval||300);
  return Math.max(5,Math.ceil(seconds/60));
 }
 function progressText(value){return Number(value?.expected)>0?` · 已处理 ${Number(value.completed||0)}/${Number(value.expected)}${value.failed?`，失败 ${Number(value.failed)}`:''}`:'';}
 function progress(host,id,value){const span=[...(host?.querySelectorAll?.('[data-watch-progress]')||[])].find(el=>el.dataset.watchProgress===id);if(span)span.textContent=progressText(value);}
 function render(watches,sources,progresses,{esc,D,form,state={}}){
  const sourceByID=new Map(sources.map(s=>[s.id,s])),progressByID=new Map(watches.map((w,i)=>[w.id,progresses[i]]));
  const options=[['ALL','全部',watches.length],['ENABLED','已启用',watches.filter(w=>w.enabled).length],['ATTENTION','需处理',watches.filter(attention).length],['PAUSED','已暂停',watches.filter(w=>!w.enabled).length],['PRIORITY','重点关注',watches.filter(w=>w.priority).length]];
  const sorted=[...watches].sort((a,b)=>Number(attention(b))-Number(attention(a))||Number(!!b.priority)-Number(!!a.priority)||String(sourceByID.get(a.source_id)?.name||a.source_id).localeCompare(String(sourceByID.get(b.source_id)?.name||b.source_id),'zh-CN'));
  return `<section id="source-watches" class="source-watches" aria-labelledby="source-watches-title"><div class="section-heading"><h3 id="source-watches-title">我的关注</h3><button id="refresh-watches" class="btn btn-small">刷新进度</button></div>${watches.length?`<div class="watch-toolbar"><div class="watch-filters" role="group" aria-label="关注状态">${options.map(([key,label,n])=>`<button type="button" class="btn btn-small" data-watch-filter="${key}" aria-pressed="${(state.status||'ALL')===key}">${label}<span>${n}</span></button>`).join('')}</div><label class="watch-search"><span class="visually-hidden">搜索我的关注</span><input type="search" data-watch-search value="${esc(state.search||'')}" placeholder="搜索已关注公司"></label></div><p class="meta" data-watch-count role="status" aria-live="polite"></p><div class="watch-list">${sorted.map(w=>{const source=sourceByID.get(w.source_id),progress=progressByID.get(w.id);return `<article class="watch-card" data-watch-id="${esc(w.id)}" ${matches(w,source,state)?'':'hidden'}><div class="watch-card-heading"><div><h4>${esc(source?.name||w.source_id)}${w.priority?'<span class="watch-priority">重点</span>':''}</h4><span class="source-status ${attention(w)?'source-manual-status':!w.enabled?'watch-paused':''}">${esc(w.enabled?outcomes[w.last_outcome]||'等待检查':'已暂停')}${!w.enabled&&attention(w)?' · 需处理':''}</span></div><button class="btn btn-small" data-source-jobs="${esc(w.source_id)}">查看岗位</button></div><p class="meta watch-timing">${w.last_checked_at?`上次 ${esc(D.date(w.last_checked_at))}`:'尚未检查'}${w.enabled?` · 下次 ${esc(D.date(w.next_check_at))}`:''}<span data-watch-progress="${esc(w.id)}">${esc(progressText(progress))}</span></p>${w.keyword?`<p class="watch-condition">筛选：${esc(w.keyword)}</p>`:''}<details class="watch-settings" data-watch-settings="${esc(w.id)}" ${state.open?.has(w.id)?'open':''}><summary>关注设置</summary>${w.effective_interval?`<p class="meta">当前每 ${Math.round(w.effective_interval/60)} 分钟检查 · ${esc(schedules[w.schedule_reason]||'等待检查')}</p>`:''}<form id="watch-${esc(w.id)}" novalidate>${form(w)}</form><button class="btn btn-small watch-delete" data-delete-watch="${esc(w.id)}">删除关注</button></details></article>`;}).join('')}</div><div class="source-empty" data-watch-empty hidden>没有符合筛选的关注。<button type="button" class="btn btn-small" data-watch-clear>清除筛选</button></div>`:'<p class="source-empty">还没有关注来源。选择下方公司，预览后开始关注。</p>'}</section>`;
 }
 function mount(host,watches,sources,state){
  if(!host?.querySelector)return;
  const search=host.querySelector('[data-watch-search]');if(!search)return;
  const cards=[...host.querySelectorAll('[data-watch-id]')],buttons=[...host.querySelectorAll('[data-watch-filter]')],byID=new Map(watches.map(w=>[w.id,w])),sourceByID=new Map(sources.map(s=>[s.id,s]));
  function refresh(){
   let visible=0;for(const card of cards){const w=byID.get(card.dataset.watchId);card.hidden=!w||!matches(w,sourceByID.get(w.source_id),state);if(!card.hidden)visible++;}
   for(const button of buttons)button.setAttribute('aria-pressed',String((state.status||'ALL')===button.dataset.watchFilter));
   const count=host.querySelector('[data-watch-count]'),empty=host.querySelector('[data-watch-empty]');if(count)count.textContent=`显示 ${visible} / ${watches.length} 个关注`;if(empty)empty.hidden=visible>0;
  }
  search.oninput=()=>{state.search=search.value;refresh();};
  for(const button of buttons)button.onclick=()=>{state.status=button.dataset.watchFilter;refresh();};
  const clear=host.querySelector('[data-watch-clear]');if(clear)clear.onclick=()=>{state.search='';state.status='ALL';search.value='';refresh();search.focus();};
  state.open||=new Set();for(const details of host.querySelectorAll('[data-watch-settings]'))details.ontoggle=()=>{if(details.open)state.open.add(details.dataset.watchSettings);else state.open.delete(details.dataset.watchSettings);};
  refresh();
 }
 const api={render,mount,matches,minimum,progress};root.CampusSourceWatches=api;if(typeof module==='object'&&module.exports)module.exports=api;
})(typeof globalThis==='undefined'?this:globalThis);
