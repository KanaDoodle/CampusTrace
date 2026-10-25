'use strict';
(function(root){
 const pageSize=24;
 function clean(kind,value={}){
  const days=(kind==='closing'?[3,7,14]:[1,7]).includes(Number(value.days))?Number(value.days):7;
  return {days,query:typeof value.query==='string'?value.query.slice(0,200):'',company:kind==='closing'&&typeof value.company==='string'?value.company.slice(0,200):'',page:Number.isInteger(value.page)&&value.page>0?Math.min(value.page,10000):1,scroll:Number.isFinite(value.scroll)?Math.min(1e7,Math.max(0,value.scroll)):0,lastJob:typeof value.lastJob==='string'?value.lastJob.slice(0,128):''};
 }
 function filter(kind,rows,state){
  const words=state.query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  return rows.filter(row=>{const job=kind==='closing'?row.job:row;return (!state.company||job.company===state.company)&&words.every(word=>[job.company,job.title,...(job.locations||[])].filter(Boolean).join(' ').toLocaleLowerCase().includes(word));});
 }
 async function page(set,{kind,query,api,esc,D,navigate,active=()=>true,cards,changes,bindActions,notify}){
  const title=kind==='closing'?'截止雷达':'最近变化',windows=kind==='closing'?[3,7,14]:[1,7],prefix=kind==='closing'?'未来':'过去';
  const cap=await api('/api/profile/resume/capabilities');if(!active())return;
  const nav=root.CampusNavigation,requested=typeof query==='object'&&query?query:query?{days:Number(query)}:{};
  let state=clean(kind,nav?.readWorkflow?.(cap.user_id,kind));
  if(requested.days&&!requested.resume)state=clean(kind,{days:requested.days});
  let rows=[],loaded=false,loadedDays=state.days,busy=false,error='',stopped=false,revision=0,composing=false;
  const pending=new Set();
  const alive=()=>!stopped&&active(),save=()=>nav?.storeWorkflow?.(cap.user_id,kind,{...state,scroll:root.scrollY||0});
  const go=(...args)=>Promise.resolve(navigate(...args)).catch(error=>{if(alive())notify(error.message);});
  if(!set(root.CampusUI.heading(title,'',`<button id="radar-browse-home" class="btn">返回今日待办</button><button id="radar-browse-refresh" class="btn">刷新</button>`)+`<section class="radar-browse"><div class="radar-browse-windows" aria-label="时间范围">${windows.map(days=>`<button type="button" data-browse-days="${days}" aria-pressed="${state.days===days}">${prefix} ${days} 天</button>`).join('')}</div><div class="radar-browse-filters"><label class="radar-browse-search"><span>${kind==='closing'?'搜索公司、岗位或城市':'搜索岗位'}</span><input id="radar-browse-search" type="search" value="${esc(state.query)}" maxlength="200" placeholder="${kind==='closing'?'例如：服务端、上海':'例如：服务端'}"></label>${kind==='closing'?'<label>公司<select id="radar-browse-company" aria-label="公司"><option value="">全部公司</option></select></label>':''}<button id="radar-browse-clear" class="text-btn">清除筛选</button></div><div id="radar-browse-results"></div></section>`))return;
  const q=selector=>root.document.querySelector(selector),host=q('#radar-browse-results'),search=q('#radar-browse-search'),company=q('#radar-browse-company');
  function draw(){
   if(!alive())return;
   const visible=filter(kind,rows,state),pages=Math.max(1,Math.ceil(visible.length/pageSize));if(loaded)state.page=Math.min(state.page,pages);
   const shown=visible.slice((state.page-1)*pageSize,state.page*pageSize),scope=prefix+' '+loadedDays+' 天';
   const message=error?`${esc(error)}${loaded?' 当前保留'+scope+'的上次读取内容。':''}`:busy?'正在读取'+prefix+' '+state.days+' 天的记录…':'';
   const html=`${message?`<p class="pending-note" role="status">${message}</p>`:''}${loaded?`<div class="radar-browse-summary"><p role="status" aria-live="polite">${scope} · 共 ${rows.length} ${kind==='closing'?'个岗位':'条变化'}${state.query||state.company?'，符合筛选 '+visible.length+' 条':''}</p>${kind==='changes'?'<span class="meta">最多显示最近 100 条变化</span>':''}</div>${shown.length?(kind==='closing'?'<div class="radar-grid">'+cards(shown,{disabled:busy||!!error,pending})+'</div>':changes(shown)):`<div class="empty-state"><h2>${rows.length?'没有符合筛选的结果':kind==='closing'?'这个时间范围内暂无待投的截止岗位':'这段时间还没有岗位变化'}</h2>${rows.length?'<button data-browse-clear class="btn">清除筛选</button>':'<button data-browse-radar class="btn">查看岗位雷达</button>'}</div>`}<div class="radar-browse-pagination"><button id="radar-browse-prev" class="btn" ${state.page<=1?'disabled':''}>上一页</button><span>第 ${state.page} / ${pages} 页${visible.length?' · '+((state.page-1)*pageSize+1)+'–'+Math.min(state.page*pageSize,visible.length)+' 条':''}</span><button id="radar-browse-next" class="btn" ${state.page>=pages?'disabled':''}>下一页</button></div>`:''}`;
   if(root.CampusUI.reconcile)root.CampusUI.reconcile(host,html);else host.innerHTML=html;
   q('#radar-browse-refresh').disabled=busy;q('#radar-browse-clear').disabled=!state.query&&!state.company;
   for(const b of root.document.querySelectorAll('[data-browse-days]')){b.disabled=busy;b.setAttribute('aria-pressed',String(Number(b.dataset.browseDays)===state.days));}
   bindActions(host,()=>load(),{active:alive,canAct:()=>!busy&&!error,pending,openJob:id=>{state.lastJob=id;save();return go('matching',{jobID:id,view:'overview',returnTo:{page:kind}});}});
   for(const b of host.querySelectorAll('[data-browse-clear]'))b.onclick=clear;
   for(const b of host.querySelectorAll('[data-browse-radar]'))b.onclick=()=>go('matching');
   for(const [id,delta] of [['radar-browse-prev',-1],['radar-browse-next',1]]){const button=q('#'+id);if(button)button.onclick=()=>{state.page=Math.max(1,Math.min(pages,state.page+delta));draw();host.scrollIntoView?.({block:'start',behavior:'instant'});save();};}
   save();
  }
  function clear(){state.query='';state.company='';state.page=1;search.value='';if(company)company.value='';draw();}
  const apply=()=>{state.query=search.value;state.page=1;draw();};
  search.oncompositionstart=()=>composing=true;
  search.oncompositionend=()=>{composing=false;apply();};
  search.oninput=event=>{state.query=search.value;save();if(!composing&&!event.isComposing)apply();};
  if(company)company.onchange=()=>{state.company=company.value;state.page=1;draw();};
  q('#radar-browse-clear').onclick=clear;q('#radar-browse-home').onclick=()=>go('radar');q('#radar-browse-refresh').onclick=()=>load();
  for(const b of root.document.querySelectorAll('[data-browse-days]'))b.onclick=()=>{state.days=Number(b.dataset.browseDays);state.page=1;load();};
  async function load(){
   if(!alive()||busy)return;const rev=++revision;busy=true;error='';draw();
   try{const value=await api('/api/radar/'+kind+'?days='+state.days);if(!alive()||rev!==revision)return;if(!Array.isArray(value))throw Error('记录暂时无法读取，请重试。');rows=value;loaded=true;loadedDays=state.days;
    if(company){const companies=[...new Set([...rows.map(row=>row.job.company),state.company].filter(Boolean))].sort((a,b)=>a.localeCompare(b,'zh'));company.innerHTML='<option value="">全部公司</option>'+companies.map(v=>`<option value="${esc(v)}" ${v===state.company?'selected':''}>${esc(v)}</option>`).join('');company.value=state.company;}
   }catch(failure){if(alive()&&rev===revision)error=failure.message;}
   finally{if(alive()&&rev===revision){busy=false;draw();}}
  }
  nav?.register({active:alive,checkpoint:save,dispose:()=>{stopped=true;revision++;}});
  const returning=requested.resume===true,returnScroll=state.scroll,returnPage=state.page;await load();
  if(alive()&&returning&&loaded){state.page=returnPage;draw();root.scrollTo?.({top:returnScroll,behavior:'instant'});const button=[...host.querySelectorAll('[data-job]')].find(b=>b.dataset.job===state.lastJob);button?.focus?.({preventScroll:true});save();}
 }
 const api={clean,filter,page,pageSize};root.CampusRadarBrowse=api;if(typeof module==='object'&&module.exports)module.exports=api;
})(typeof window==='undefined'?globalThis:window);
