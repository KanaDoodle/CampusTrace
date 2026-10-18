(function(root){
 'use strict';
 const labels={PENDING:'等待开始',PREPARING:'准备来源',QUEUED:'等待读取',FETCHING:'导入岗位',RETRYING:'自动重试中',SUCCESS:'已完成',EMPTY:'暂无岗位',FAILED:'需要重试'};
 const errors={SOURCE_IMPORT_NETWORK:'连接超时或网络异常',SOURCE_IMPORT_BLOCKED:'官网限制了访问',SOURCE_IMPORT_BUSY:'官网暂时繁忙',SOURCE_IMPORT_CAPACITY:'岗位数超过单来源上限，可先到官网查看',SOURCE_IMPORT_CHANGED:'官网返回格式或招聘范围有变化',SOURCE_IMPORT_FAILED:'暂时无法读取来源',SOURCE_IMPORT_DISCOVERY_FAILED:'暂时无法读取完整岗位列表',SOURCE_IMPORT_PARTIAL_FAILURE:'部分岗位未能读取，已保留成功项',SOURCE_IMPORT_INTERRUPTED:'恢复副本中的导入已中断，可重新开始'};
 const status=item=>item.state==='FAILED'?'FAILED':['SUCCESS','EMPTY'].includes(item.state)?'DONE':'ACTIVE';
 function select(items,state={}){
  const query=String(state.search||'').trim().toLocaleLowerCase();
  return items.filter(v=>(!state.filter||state.filter==='ALL'||status(v)===state.filter)&&(!query||String(v.company||'').toLocaleLowerCase().includes(query)));
 }
 function mount(host,{api,esc,D,active=()=>true,navigate,count=0,state={}}){
  if(!host)return ()=>{};
  state.search||='';state.filter||='ALL';
  let batch=null,busy=false,stopped=false,timer=null,loading=true,warning='',opened=!!state.opened,revision=0,composing=false,drawPending=false;
  const alive=()=>!stopped&&active();
  const clear=()=>{if(timer!==null){clearTimeout(timer);timer=null;}};
  const rememberScroll=()=>{const list=host.querySelector('[data-import-rows]');if(list)state.scroll=list.scrollTop;};
  function rowsHTML(){
   const items=batch?.items||[],rows=select(items,state);
   return rows.length?rows.map(v=>`<div class="source-bulk-row"><div><b>${esc(v.company)}</b><small>${esc(v.scope)}</small>${v.code&&v.state==='FAILED'?`<small class="pending-note">${esc(errors[v.code]||errors.SOURCE_IMPORT_FAILED)}</small>`:''}</div><div><span class="source-bulk-status ${v.state==='FAILED'?'is-failed':''}">${esc(labels[v.state]||'等待更新')}</span><small>${v.expected?`${Number(v.completed||0)}/${Number(v.expected)} 个岗位${v.failed?`，失败 ${Number(v.failed)}`:''}`:''}</small>${v.source_id?`<button class="btn btn-small" data-import-source="${esc(v.source_id)}">查看岗位</button>`:''}</div></div>`).join(''):'<p class="source-empty">没有符合筛选的来源。</p>';
  }
  function bindRows(){for(const b of host.querySelectorAll('[data-import-source]'))b.onclick=()=>navigate('source_jobs',{sourceID:b.dataset.importSource,page:1});}
  function filterRows(){
   const list=host.querySelector('[data-import-rows]');if(!list)return;
   list.innerHTML=rowsHTML();list.scrollTop=0;state.scroll=0;
   for(const b of host.querySelectorAll('[data-import-filter]'))b.setAttribute('aria-pressed',String(state.filter===b.dataset.importFilter));
   const shown=host.querySelector('[data-import-count]');if(shown)shown.textContent=`显示 ${select(batch?.items||[],state).length} / ${(batch?.items||[]).length} 个来源`;
   bindRows();
  }
  function draw(){
   if(!alive())return;
   if(composing){drawPending=true;return;}
   drawPending=false;
   rememberScroll();
   const oldSearch=host.querySelector('[data-import-search]'),focused=oldSearch&&host.ownerDocument?.activeElement===oldSearch;
   const caret=focused?[oldSearch.selectionStart,oldSearch.selectionEnd]:null;
   const running=batch?.state==='RUNNING',items=batch?.items||[],done=Number(batch?.completed||0),failed=Number(batch?.failed||0),total=items.length;
   const actionDisabled=busy||loading||count===0;
   const filters=[['ALL','全部',total],['FAILED','需重试',items.filter(v=>status(v)==='FAILED').length],['ACTIVE','进行中',items.filter(v=>status(v)==='ACTIVE').length],['DONE','已完成',items.filter(v=>status(v)==='DONE').length]];
   host.innerHTML=`<section class="source-bulk" aria-labelledby="bulk-source-title">
    <div class="source-bulk-heading"><div><h3 id="bulk-source-title">批量导入招聘来源</h3><p class="meta">全部 ${count} 个已适配来源 · 已有岗位自动跳过 · 离开页面仍会继续</p></div><button class="btn btn-primary" data-import-all ${actionDisabled?'disabled':''}>${busy?'提交中…':running?'查看当前导入':'一键导入全部岗位'}</button></div>
    ${warning?`<p class="pending-note" role="status">${esc(warning)} <button class="btn btn-small" data-import-error-refresh>刷新进度</button></p>`:''}
    ${batch?`<div class="source-bulk-progress" role="status"><b>${running?'正在后台导入':failed?'本轮导入结束，有来源需要重试':'本轮导入完成'}</b><span>来源 ${done}/${total} · 已读取 ${Number(batch.imported||0)} 个岗位${failed?` · 失败 ${failed} 个来源`:''}</span><progress max="${total||1}" value="${done}" aria-label="招聘来源导入进度"></progress></div>
    <div class="actions">${failed?`<button class="btn" data-import-retry ${busy?'disabled':''}>重试失败来源（${failed}）</button>`:''}<button class="btn" data-import-jobs>去岗位雷达</button><button class="btn btn-small" data-import-refresh ${busy?'disabled':''}>刷新进度</button></div>
    <details class="source-bulk-details" ${opened?'open':''}><summary>各来源进度 <span class="meta">${esc(D.date(batch.created_at))}</span></summary>
     <div class="source-bulk-filterbar"><div class="watch-filters" role="group" aria-label="导入状态">${filters.map(([key,label,n])=>`<button type="button" class="btn btn-small" data-import-filter="${key}" aria-pressed="${state.filter===key}">${label}<span>${n}</span></button>`).join('')}</div><label><span class="visually-hidden">搜索导入来源</span><input type="search" data-import-search value="${esc(state.search)}" placeholder="搜索本轮公司"></label></div>
     <p class="meta" data-import-count role="status" aria-live="polite">显示 ${select(items,state).length} / ${total} 个来源</p><div class="source-bulk-list" data-import-rows>${rowsHTML()}</div>
    </details>`:loading?'<p class="meta">正在读取导入进度…</p>':''}
   </section>`;
   const bind=(selector,fn)=>{const el=host.querySelector(selector);if(el)el.onclick=fn;};
   bind('[data-import-all]',()=>running?(opened=state.opened=true,draw()):submit('/api/sources/import-all'));
   bind('[data-import-retry]',()=>submit('/api/sources/imports/'+encodeURIComponent(batch.id)+'/retry'));
   bind('[data-import-refresh]',()=>refresh());
   bind('[data-import-error-refresh]',()=>refresh());
   bind('[data-import-jobs]',()=>navigate('matching'));
   bindRows();
   const detail=host.querySelector('details');if(detail)detail.ontoggle=()=>{opened=state.opened=detail.open;};
   for(const b of host.querySelectorAll('[data-import-filter]'))b.onclick=()=>{state.filter=b.dataset.importFilter;filterRows();};
   const search=host.querySelector('[data-import-search]');if(search){
    search.oncompositionstart=()=>{composing=true;};
    search.oninput=e=>{if(e?.isComposing)composing=true;if(composing)return;state.search=search.value;filterRows();};
    search.oncompositionend=()=>{composing=false;state.search=search.value;if(drawPending)draw();else filterRows();};
    if(focused){search.focus({preventScroll:true});if(caret?.[0]!=null)search.setSelectionRange?.(...caret);}
   }
   const list=host.querySelector('[data-import-rows]');if(list){list.scrollTop=Number(state.scroll||0);list.onscroll=()=>{state.scroll=list.scrollTop;};}
  }
  function poll(){clear();if(alive()&&batch?.state==='RUNNING')timer=setTimeout(()=>refresh(),5000);}
  async function refresh(){
   if(!alive()||busy)return;clear();const requestRevision=++revision;
   try{const v=await api('/api/sources/imports/latest');if(!alive()||busy||requestRevision!==revision)return;batch=v;warning='';}
   catch(e){if(alive()&&!busy&&requestRevision===revision)warning=e.message||'读取进度失败，请刷新重试。';}
   finally{if(alive()&&!busy&&requestRevision===revision){loading=false;draw();poll();}}
  }
  async function submit(url){
   if(!alive()||busy)return;busy=true;++revision;clear();warning='';draw();
   try{const v=await api(url,'POST',{});if(!alive())return;batch=v;opened=state.opened=true;}
   catch(e){if(alive())warning=e.code==='SOURCE_IMPORT_COOLDOWN'?'完整导入每 30 分钟可发起一次；失败来源可以单独重试。':e.message||'未能提交导入，请稍后重试。';}
   finally{busy=false;if(alive()){loading=false;draw();poll();}}
  }
  draw();refresh();
  return ()=>{rememberScroll();stopped=true;clear();};
 }
 const api={mount,select};root.CampusSourceBulk=api;if(typeof module==='object'&&module.exports)module.exports=api;
})(globalThis);
