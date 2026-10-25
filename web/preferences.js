'use strict';
(function(root){
 function clean(value={}){return {stage:value.stage==='IGNORED'?'IGNORED':'SAVED',query:typeof value.query==='string'?value.query.slice(0,200):'',jobSort:['company','title'].includes(value.jobSort)?value.jobSort:'updated',page:Number.isInteger(value.page)&&value.page>0?Math.min(value.page,20000):1,lastJob:typeof value.lastJob==='string'?value.lastJob.slice(0,128):'',scroll:Number.isFinite(value.scroll)?Math.max(0,Math.min(1e7,value.scroll)):0,listScroll:Number.isFinite(value.listScroll)?Math.max(0,Math.min(1e7,value.listScroll)):0};}
 async function page(set,{api,esc,D,navigate,query,active=()=>true,notify=()=>{}}){
  const cap=await api('/api/profile/resume/capabilities');if(!active())return;
  const nav=root.CampusNavigation,C=root.CampusMatchingChat,selected=C.readSelection(cap.user_id);
  const state=clean(nav?.readWorkflow(cap.user_id,'preferences')),key=()=>JSON.stringify([state.stage,state.query,state.jobSort,state.page]);
  let packet=null,loadedState=null,busy=false,writing=false,stale=true,error='',actionError='',revision=0,pendingKey='',stopped=false,composing=false,timer;
  const alive=()=>!stopped&&active(),labels={SAVED:'稍后看',IGNORED:'已忽略'},save=()=>nav?.storeWorkflow(cap.user_id,'preferences',{...state,scroll:root.scrollY||0,listScroll:root.document.querySelector('#preference-list')?.scrollTop||0});
  const go=(...args)=>Promise.resolve(navigate(...args)).catch(failure=>{if(alive())notify(failure.message);});
  if(!set(root.CampusUI.heading('稍后看','',`<button id="preference-radar" class="btn">岗位雷达</button><button id="preference-refresh" class="btn">刷新</button>`)+`<section class="preferences"><div class="preference-toolbar"><nav class="radar-browse-views" aria-label="收藏范围"><button id="preference-saved" type="button">稍后看</button><button id="preference-ignored" type="button">已忽略</button></nav><label class="preference-search"><span class="visually-hidden">搜索收藏岗位</span><input id="preference-search" type="search" maxlength="200" placeholder="搜索公司、岗位或城市" value="${esc(state.query)}"></label><label class="preference-sort">排序<select id="preference-sort" aria-label="收藏岗位排序"><option value="updated">岗位最近更新</option><option value="company" ${state.jobSort==='company'?'selected':''}>公司名称</option><option value="title" ${state.jobSort==='title'?'selected':''}>岗位名称</option></select></label><button id="preference-clear" class="text-btn">清空搜索</button></div><div id="preference-results"></div></section>`))return;
  const q=s=>root.document.querySelector(s),host=q('#preference-results'),search=q('#preference-search');
  const canAct=()=>!!packet&&!busy&&!writing&&!stale&&!error&&!composing;
  function draw(){
   if(!alive())return;
   const rows=packet?.jobs||[],checked=rows.filter(row=>selected.has(row.job.id)),readable=canAct();
   const message=error?error+(packet?' 当前保留上次读取的'+labels[loadedState.stage]+'列表。':''):busy||stale?'正在读取'+labels[state.stage]+'…':'';
   const html=`${message?`<p class="pending-note" role="status">${esc(message)}</p>`:''}${actionError?`<p class="pending-note" role="status">${esc(actionError)}</p>`:''}${error?'<button id="preference-retry" class="btn btn-small">重新读取列表</button>':''}${packet?`<div class="preference-summary"><label class="quiet-check"><input id="preference-select-page" type="checkbox" aria-label="全选或取消本页收藏岗位" ${rows.length&&checked.length===rows.length?'checked':''} ${!readable||!rows.length?'disabled':''}>${esc(labels[loadedState.stage])} · ${packet.total} 个岗位${loadedState.query?'符合“'+esc(loadedState.query)+'”':''}</label><div><span>本页已选 ${checked.length} 个</span><button id="preference-open-selected" class="text-btn" ${!selected.size?'disabled':''}>在雷达查看已选（${selected.size}）</button>${loadedState.stage==='IGNORED'?`<button id="preference-restore-page" class="text-btn" ${!readable||!checked.length?'disabled':''}>恢复本页已选（${checked.length}）</button>`:''}</div></div><div id="preference-list" class="preference-list workbench-job-list" data-scroll-region>${rows.length?rows.map(row=>`<article class="preference-item" data-workbench-row="preference-${esc(row.job.id)}"><label class="quiet-check"><input type="checkbox" data-preference-select="${esc(row.job.id)}" aria-label="选择 ${esc(row.job.company)} ${esc(row.job.title)}" ${selected.has(row.job.id)?'checked':''} ${!readable?'disabled':''}></label><div class="preference-item-main"><p class="meta">${esc(D.text(row.job.company))}${row.application?' · '+esc(D.label('application',row.application.current_state)):''}</p><h2><button class="job-link" data-preference-job="${esc(row.job.id)}">${esc(D.text(row.job.title))}</button></h2><p class="meta">${esc((row.job.locations||[]).join('、')||'地点未注明')} · ${esc(D.label('job',row.job.current_status))}${row.job.updated_at?' · 更新于 '+esc(D.date(row.job.updated_at)):''}</p></div><button class="text-btn preference-remove" data-preference-remove="${esc(row.job.id)}" ${!readable?'disabled':''}>${loadedState.stage==='SAVED'?'取消稍后看':'恢复岗位'}</button></article>`).join(''):`<div class="empty-state"><h2>${loadedState.query?'没有符合搜索的岗位':loadedState.stage==='SAVED'?'还没有稍后看的岗位':'没有已忽略的岗位'}</h2>${loadedState.query?'<button data-preference-clear class="btn">清空搜索</button>':'<button data-preference-radar class="btn">去岗位雷达看看</button>'}</div>`}</div><div class="radar-browse-pagination"><button id="preference-prev" class="btn" ${busy||writing||stale||!!error||packet.page<=1?'disabled':''}>上一页</button><span>第 ${packet.page} / ${Math.max(1,Math.ceil(packet.total/packet.page_size))} 页</span><button id="preference-next" class="btn" ${busy||writing||stale||!!error||packet.page*packet.page_size>=packet.total?'disabled':''}>下一页</button></div>`:''}`;
   const scroll=q('#preference-list')?.scrollTop||0;root.CampusUI.reconcile(host,html);const list=q('#preference-list');if(list)list.scrollTop=scroll;
   q('#preference-refresh').disabled=busy||writing;q('#preference-clear').disabled=!search.value||writing;q('#preference-sort').disabled=search.disabled=writing;
   q('#preference-saved').setAttribute('aria-pressed',String(state.stage==='SAVED'));q('#preference-ignored').setAttribute('aria-pressed',String(state.stage==='IGNORED'));
   q('#preference-saved').disabled=q('#preference-ignored').disabled=writing;
   const retry=q('#preference-retry');if(retry){retry.disabled=busy||writing;retry.onclick=()=>load();}
   for(const b of host.querySelectorAll('[data-preference-job]'))b.onclick=()=>{if(!alive())return;state.lastJob=b.dataset.preferenceJob;save();return go('matching',{jobID:b.dataset.preferenceJob,view:'overview',returnTo:{page:'preferences'}});};
   for(const b of host.querySelectorAll('[data-preference-remove]'))b.onclick=()=>write([b.dataset.preferenceRemove],false);
   for(const input of host.querySelectorAll('[data-preference-select]'))input.onchange=()=>{if(!canAct())return;if(input.checked)selected.add(input.dataset.preferenceSelect);else selected.delete(input.dataset.preferenceSelect);C.storeSelection(cap.user_id,selected);draw();};
   for(const b of host.querySelectorAll('[data-preference-clear]'))b.onclick=clear;
   for(const b of host.querySelectorAll('[data-preference-radar]'))b.onclick=()=>go('matching');
   const select=q('#preference-select-page');if(select)select.onchange=()=>{if(!canAct())return;for(const row of rows)if(select.checked)selected.add(row.job.id);else selected.delete(row.job.id);C.storeSelection(cap.user_id,selected);draw();};
   const open=q('#preference-open-selected');if(open)open.onclick=()=>{if(selected.size)return go('matching',{selected:true});};
   const restore=q('#preference-restore-page');if(restore)restore.onclick=()=>write(checked.map(row=>row.job.id),true);
   for(const [id,delta] of [['preference-prev',-1],['preference-next',1]]){const b=q('#'+id);if(b)b.onclick=()=>{if(!canAct())return;state.page=Math.max(1,Math.min(Math.ceil(packet.total/packet.page_size),packet.page+delta));return load(true);};}
   save();
  }
  async function load(resetScroll=false){
   if(!alive()||writing)return;const requested=key();if(busy&&requested===pendingKey)return;
   clearTimeout(timer);const rev=++revision,input={...state};pendingKey=requested;busy=true;stale=true;error='';draw();
   try{
    const params=new URLSearchParams({disposition:input.stage,q:input.query,sort:input.jobSort,page:String(input.page)}),value=await api('/api/preferences/jobs?'+params);
    if(!alive()||rev!==revision||requested!==key())return;
    if(!Array.isArray(value?.jobs)||!Number.isSafeInteger(value.total)||value.total<0||!Number.isInteger(value.page)||value.page<1||value.page_size!==50||value.jobs.length>50)throw Error('收藏岗位列表暂时无法读取，请重试。');
    packet=value;state.page=value.page;loadedState=input;stale=false;actionError='';
    if(resetScroll){const list=q('#preference-list');if(list)list.scrollTop=0;}
   }catch(failure){if(alive()&&rev===revision&&requested===key())error=failure.message;}
   finally{if(alive()&&rev===revision){busy=false;pendingKey='';draw();}}
  }
  async function write(ids,bulk){
   if(!canAct()||!ids.length)return;const allowed=new Set(packet.jobs.map(row=>row.job.id));if(ids.some(id=>!allowed.has(id)))return;
   writing=true;actionError='';draw();
   try{
    let message;
    if(bulk){const out=await api('/api/jobs/preferences','PUT',{job_ids:ids,disposition:'NONE'});message='已恢复 '+out.updated.length+' 个岗位。'+(out.skipped.length?'另有 '+out.skipped.length+' 个岗位状态已变化，未修改。':'');}
    else{await api('/api/jobs/'+encodeURIComponent(ids[0])+'/preference','PUT',{disposition:'NONE'});message=loadedState.stage==='SAVED'?'已取消稍后看。':'已恢复岗位。';}
    if(!alive())return;notify(message);writing=false;stale=true;await load();
   }catch(failure){if(alive())actionError=failure.message;}
   finally{writing=false;if(alive())draw();}
  }
  function schedule(){if(writing)return;state.query=search.value;state.page=1;stale=true;error='';clearTimeout(timer);draw();timer=setTimeout(()=>load(true),240);}
  function clear(){if(writing)return;search.value='';state.query='';state.page=1;return load(true);}
  search.oncompositionstart=()=>{composing=true;clearTimeout(timer);};search.oninput=event=>{if(!composing&&!event.isComposing)schedule();};search.oncompositionend=()=>{composing=false;schedule();};
  q('#preference-sort').onchange=()=>{if(writing)return;state.jobSort=q('#preference-sort').value;state.page=1;return load(true);};
  for(const [id,stage] of [['preference-saved','SAVED'],['preference-ignored','IGNORED']])q('#'+id).onclick=()=>{if(writing)return;state.stage=stage;state.page=1;return load(true);};
  q('#preference-clear').onclick=clear;q('#preference-refresh').onclick=()=>load();q('#preference-radar').onclick=()=>go('matching');
  nav?.register({active:alive,checkpoint:save,dispose:()=>{stopped=true;revision++;clearTimeout(timer);}});
  const scroll=state.scroll,listScroll=state.listScroll;await load();
  if(alive()&&packet&&query?.resume){root.scrollTo?.({top:scroll,behavior:'instant'});const list=q('#preference-list');if(list)list.scrollTop=listScroll;[...host.querySelectorAll('[data-preference-job]')].find(b=>b.dataset.preferenceJob===state.lastJob)?.focus?.({preventScroll:true});save();}
 }
 const api={clean,page};root.CampusPreferences=api;if(typeof module==='object'&&module.exports)module.exports=api;
})(typeof window==='undefined'?globalThis:window);
