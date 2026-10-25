'use strict';
(function(root){
 const pageSize=20;
 function clean(value={}){
  return {query:typeof value.query==='string'?value.query.slice(0,200):'',view:value.view==='unread'?'unread':'all',page:Number.isInteger(value.page)&&value.page>0?Math.min(value.page,10000):1,scroll:Number.isFinite(value.scroll)?Math.max(0,Math.min(value.scroll,1e7)):0,lastNotification:typeof value.lastNotification==='string'?value.lastNotification.slice(0,128):''};
 }
 function filter(rows,state){
  const words=state.query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  return rows.filter(row=>(state.view!=='unread'||!row.read_at)&&words.every(word=>[row.title,row.body].join(' ').toLocaleLowerCase().includes(word)));
 }
 async function page(set,{api,esc,D,navigate,query,active=()=>true,notify=()=>{}}){
  // Begin the bounded inbox read alongside identity lookup; never generate reminders on entry.
  const firstRead=api('/api/notifications').then(value=>({value}),error=>({error}));
  const cap=await api('/api/profile/resume/capabilities');if(!active())return;
  const nav=root.CampusNavigation;
  let state=clean(nav?.readWorkflow(cap.user_id,'notifications')),rows=[],loaded=false,busy=false,error='',stopped=false,composing=false;
  const pending=new Set(),rowErrors=new Map(),alive=()=>!stopped&&active();
  const save=()=>nav?.storeWorkflow(cap.user_id,'notifications',{...state,scroll:root.scrollY||0});
  const go=(...args)=>Promise.resolve(navigate(...args)).catch(failure=>{if(alive())notify(failure.message);});
  if(!set(root.CampusUI.heading('通知收件箱','',`<button id="inbox-refresh" class="btn">刷新列表</button><button id="inbox-check" class="btn">检查新提醒</button>`)+`<section class="inbox"><div class="inbox-toolbar"><div class="radar-browse-views" aria-label="通知范围"><button id="inbox-all" type="button" aria-pressed="${state.view==='all'}">全部 <span id="inbox-all-count"></span></button><button id="inbox-unread" type="button" aria-pressed="${state.view==='unread'}">未读 <span id="inbox-unread-count"></span></button></div><label class="inbox-search"><span class="visually-hidden">搜索通知</span><input id="inbox-search" type="search" maxlength="200" value="${esc(state.query)}" placeholder="搜索通知"></label><button id="inbox-clear" class="text-btn">清除搜索</button></div><div id="inbox-results"></div></section>`))return;
  const q=selector=>root.document.querySelector(selector),host=q('#inbox-results'),search=q('#inbox-search');
  const visibleRows=()=>filter(rows,state),shownRows=()=>visibleRows().slice((state.page-1)*pageSize,state.page*pageSize);
  const rowElements=()=>[...host.querySelectorAll('[data-notification]')];
  function focusRow(id,readFirst=false){const article=rowElements().find(el=>el.dataset.notification===id),primary=readFirst?'[data-notification-read]':'[data-notification-job]',secondary=readFirst?'[data-notification-job]':'[data-notification-read]';(article?.querySelector(primary)||article?.querySelector(secondary)||article)?.focus?.({preventScroll:true});}
  function draw(){
   if(!alive())return;
   const visible=visibleRows(),pages=Math.max(1,Math.ceil(visible.length/pageSize));if(loaded)state.page=Math.min(state.page,pages);
   const shown=shownRows(),unread=rows.filter(row=>!row.read_at).length;
   const message=error?error+(loaded?' 当前保留上次读取的通知。':''):busy?'正在读取通知…':'';
   const html=`${message?`<p class="pending-note" role="status">${esc(message)}</p>`:''}${error?'<button id="inbox-retry" class="btn btn-small">重新读取列表</button>':''}${loaded?`<p class="inbox-summary" role="status" aria-live="polite">${rows.length===100?'最近 100 条':'共 '+rows.length+' 条'} · 未读 ${unread} 条${state.query||state.view==='unread'?' · 当前筛选 '+visible.length+' 条':''}</p><div class="inbox-list">${shown.length?shown.map(row=>`<article class="inbox-item ${row.read_at?'inbox-read':'inbox-unread'}" data-workbench-row="notification-${esc(row.id)}" data-notification="${esc(row.id)}" tabindex="-1"><div class="inbox-item-main"><div class="inbox-item-heading"><h2>${!row.read_at?'<span class="inbox-dot" aria-label="未读"></span>':''}${esc(row.title)}</h2><time datetime="${esc(row.created_at)}">${esc(D.date(row.created_at))}</time></div><p>${esc(row.body)}</p>${rowErrors.has(row.id)?`<p class="inbox-error" role="status">${esc(rowErrors.get(row.id))}</p>`:''}<div class="inbox-actions">${row.entity_type==='JOB'?`<button class="text-btn" data-notification-job="${esc(row.id)}" data-job-id="${esc(row.entity_id)}">查看岗位</button>`:''}${!row.read_at?`<button class="text-btn" data-notification-read="${esc(row.id)}" ${busy||!!error||pending.has(row.id)?'disabled':''}>${pending.has(row.id)?'正在标记…':rowErrors.has(row.id)?'重试标为已读':'标为已读'}</button>`:'<span class="inbox-read-label">已读</span>'}</div></div></article>`).join(''):`<div class="empty-state"><h2>${state.query?'没有找到这条通知':state.view==='unread'?'没有未读通知':'暂时没有通知'}</h2>${state.query?'<button data-inbox-clear class="btn">清除搜索</button>':state.view==='unread'?'<button data-inbox-all class="btn">查看全部通知</button>':'<button data-inbox-watches class="btn">查看关注源</button>'}</div>`}</div><div class="radar-browse-pagination"><button id="inbox-prev" class="btn" ${state.page<=1?'disabled':''}>上一页</button><span>第 ${state.page} / ${pages} 页</span><button id="inbox-next" class="btn" ${state.page>=pages?'disabled':''}>下一页</button></div>`:''}`;
   root.CampusUI.reconcile(host,html);
   q('#inbox-refresh').disabled=q('#inbox-check').disabled=busy||pending.size>0;
   q('#inbox-clear').disabled=!search.value;
   q('#inbox-all').setAttribute('aria-pressed',String(state.view==='all'));q('#inbox-unread').setAttribute('aria-pressed',String(state.view==='unread'));
   q('#inbox-all-count').textContent=loaded?String(rows.length):'';q('#inbox-unread-count').textContent=loaded?String(unread):'';
   const retry=q('#inbox-retry');if(retry){retry.disabled=busy||pending.size>0;retry.onclick=()=>load();}
   for(const b of host.querySelectorAll('[data-notification-read]'))b.onclick=()=>markRead(b);
   for(const b of host.querySelectorAll('[data-notification-job]'))b.onclick=()=>{if(!alive())return;state.lastNotification=b.dataset.notificationJob;save();return go('matching',{jobID:b.dataset.jobId,view:'overview',returnTo:{page:'notifications'}});};
   for(const b of host.querySelectorAll('[data-inbox-clear]'))b.onclick=clear;
   for(const b of host.querySelectorAll('[data-inbox-all]'))b.onclick=()=>view('all');
   for(const b of host.querySelectorAll('[data-inbox-watches]'))b.onclick=()=>go('watches');
   for(const [id,delta] of [['inbox-prev',-1],['inbox-next',1]]){const b=q('#'+id);if(b)b.onclick=()=>{state.page=Math.max(1,Math.min(pages,state.page+delta));draw();host.scrollIntoView?.({block:'start',behavior:'instant'});save();};}
   save();
  }
  function view(value){state.view=value;state.page=1;draw();}
  function clear(){state.query='';state.page=1;search.value='';draw();}
  async function markRead(button){
   const id=button.dataset.notificationRead;if(!alive()||busy||error||pending.has(id)||!rows.some(row=>row.id===id&&!row.read_at))return;
   const focused=root.document.activeElement===button,index=shownRows().findIndex(row=>row.id===id);
   pending.add(id);rowErrors.delete(id);draw();
   try{
    const result=await api('/api/notifications/'+encodeURIComponent(id)+'/read','POST',{});if(!alive())return;
    if(result?.read!==true)throw Error('未能确认已读状态，请重试。');
    rows=rows.map(row=>row.id===id?{...row,read_at:new Date().toISOString()}:row);
   }catch(failure){if(alive())rowErrors.set(id,failure.message);}
   finally{pending.delete(id);if(alive()){const restoreFocus=focused&&root.document.activeElement===button;draw();if(restoreFocus){const shown=shownRows(),next=shown.find(row=>row.id===id)||shown[Math.min(Math.max(index,0),shown.length-1)];if(next)focusRow(next.id,true);else q('#inbox-unread').focus?.({preventScroll:true});}}}
  }
  async function load(initial=null,check=false){
   if(!alive()||busy||pending.size)return;busy=true;error='';draw();let checked=null;
   try{
    if(check){checked=await api('/api/notifications/refresh','POST',{});if(!alive())return;}
    const result=initial?await initial:{value:await api('/api/notifications')};if(!alive())return;
    if(result.error)throw result.error;if(!Array.isArray(result.value))throw Error('通知列表暂时无法读取，请重试。');
    rows=result.value;loaded=true;rowErrors.clear();
    if(checked)notify(Number(checked.created)>0?'新增 '+Number(checked.created)+' 条提醒。':'已检查，暂无新提醒。');
   }catch(failure){if(alive())error=(checked?'已检查新提醒，但列表读取失败。':'')+failure.message;}
   finally{if(alive()){busy=false;draw();}}
  }
  const apply=()=>{state.query=search.value;state.page=1;draw();};
  search.oncompositionstart=()=>composing=true;search.oncompositionend=()=>{composing=false;apply();};
  search.oninput=event=>{if(!composing&&!event.isComposing)apply();};
  q('#inbox-clear').onclick=clear;q('#inbox-all').onclick=()=>view('all');q('#inbox-unread').onclick=()=>view('unread');
  q('#inbox-refresh').onclick=()=>load();q('#inbox-check').onclick=()=>load(null,true);
  nav?.register({active:alive,checkpoint:save,dispose:()=>stopped=true});
  const scroll=state.scroll;await load(firstRead);
  if(alive()&&loaded&&query?.resume){root.scrollTo?.({top:scroll,behavior:'instant'});focusRow(state.lastNotification);save();}
 }
 const api={clean,filter,page,pageSize};root.CampusNotifications=api;if(typeof module==='object'&&module.exports)module.exports=api;
})(typeof window==='undefined'?globalThis:window);
