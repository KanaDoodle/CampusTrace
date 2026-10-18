'use strict';
(function(root){
 function urgency(at,now){const hours=(Date.parse(at)-now)/3600000;return hours<=24?'24 小时内':hours<=72?'3 天内':'7 天内';}
 function timeLabel(at){const parts=new Intl.DateTimeFormat('zh-CN',{timeZone:'Asia/Shanghai',month:'numeric',day:'numeric',hour:'2-digit',minute:'2-digit',hour12:false}).formatToParts(new Date(at)),value=type=>parts.find(v=>v.type===type)?.value;return value('month')+'月'+value('day')+'日 '+value('hour')+':'+value('minute');}
 function render({snapshot=null,busy=false,error=''}={}, {esc,D}){
  const now=Date.now(),items=(snapshot?.items||[]).filter(v=>Date.parse(v.at)>now);
  return `<section id="deadline-overview" class="deadline-panel" aria-label="即将截止"><div class="deadline-heading"><h2>${root.CampusUI?.icon('clock')||''}即将截止</h2>${snapshot?`<p class="deadline-counts"><span class="${snapshot.within_day?'is-urgent':''}">24 小时内 <strong>${Number(snapshot.within_day)||0}</strong></span><span>3 天内 <strong>${Number(snapshot.within_3_days)||0}</strong></span><span>7 天内 <strong>${Number(snapshot.total)||0}</strong></span></p>`:''}<span class="deadline-timezone">北京时间</span><div class="deadline-tools"><button type="button" class="text-btn" data-deadline-all>查看全部</button><button type="button" class="text-btn" data-deadline-refresh ${busy?'disabled':''}>${busy?'更新中…':'刷新'}</button></div></div>${error?`<p class="deadline-message" role="status">${esc(error)}${snapshot?' 当前保留上次读取内容。':''}</p>`:''}${items.length?`<ul class="deadline-list">${items.map(v=>`<li class="${Date.parse(v.at)-now<=86400000?'is-urgent':''}"><button type="button" class="deadline-job" data-deadline-job="${esc(v.job.id)}"><span class="deadline-company">${esc(v.job.company)}</span><strong title="${esc(v.job.title)}">${esc(v.job.title)}</strong><span class="deadline-at" title="${esc(v.date?v.date+'（官网日期）':D.date(v.at))}">${esc(v.date?v.date+' 当天结束':timeLabel(v.at))}</span><span class="deadline-window">${urgency(v.at,now)}${v.planned?' · 已计划':v.saved?' · 稍后看':''}</span></button></li>`).join('')}</ul>`:!error?`<p class="deadline-message" role="status">${snapshot?'未来 7 天暂无已确认截止时间的待投岗位。':'正在读取截止提醒…'}</p>`:''}</section>`;
 }
 function create({api,esc,D,navigate,active=()=>true,onChange=()=>{},onJob}){
  let state={snapshot:null,busy:false,error:''},stopped=false,timer=null,revision=0;
  const alive=()=>!stopped&&active();
  const changed=()=>{if(alive())onChange();};
  const go=(...args)=>Promise.resolve(navigate(...args)).catch(error=>{if(alive()){state.error=error.message;changed();}});
  async function load(){
   if(!alive()||state.busy)return;const rev=++revision;state.busy=true;state.error='';changed();
   try{const v=await api('/api/radar/deadlines');if(!alive()||rev!==revision)return;if(!Array.isArray(v?.items))throw Error('截止提醒暂时无法读取，请重试。');state.snapshot=v;}
   catch(error){if(alive()&&rev===revision)state.error=error.message;}
   finally{if(alive()&&rev===revision){state.busy=false;changed();}}
  }
  function bind(host){
   if(!host||!alive())return;
   host.querySelector('[data-deadline-refresh]').onclick=()=>load();
   host.querySelector('[data-deadline-all]').onclick=()=>go('closing',7);
   for(const b of host.querySelectorAll('[data-deadline-job]'))b.onclick=()=>{if(onJob)Promise.resolve(onJob(b.dataset.deadlineJob)).catch(error=>{if(alive()){state.error=error.message;changed();}});else go('matching',{jobID:b.dataset.deadlineJob,view:'overview'});};
  }
  async function tick(){if(!alive())return;if(root.document?.visibilityState!=='hidden')await load();if(alive())timer=setTimeout(tick,60000);}
  return {html:()=>render(state,{esc,D}),bind,load,start:tick,dispose(){stopped=true;revision++;clearTimeout(timer);}};
 }
 function mount(host,helpers){
  if(!host)return;const panel=create({...helpers,onChange:()=>{host.innerHTML=panel.html();panel.bind(host);}});
  host.innerHTML=panel.html();panel.bind(host);panel.start();return panel;
 }
 const api={render,create,mount,urgency};root.CampusDeadlines=api;if(typeof module==='object'&&module.exports)module.exports=api;
})(typeof window==='undefined'?globalThis:window);
