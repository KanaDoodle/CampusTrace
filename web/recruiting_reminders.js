(function(root){
'use strict';
// One Today-page visit owns the read/write promise. Redraws replace only the
// current painter, so an old request can update the new host, never a dead one.
function mount(host,{api,esc,D,navigate,active=()=>true,state={}}){
 let disposed=false;
 const alive=()=>!disposed&&active();
 function update(action){
  if(!alive())return;
  if(state.pending)return action?undefined:state.pending;
  const work=(async()=>{try{if(action)await action();const value=await api('/api/agent/todos');if(!value||typeof value.enabled!=='boolean'||!Array.isArray(value.items))throw Error('招聘变化响应暂时无法识别，请重试。');if(active()){state.value=value;state.error='';}}catch(e){if(active())state.error=e.message||(action?'暂时无法保存，请重试。':'招聘变化暂时无法读取。');}})();
  state.pending=work.finally(()=>{state.pending=null;state.paint?.();});
  for(const button of host.querySelectorAll('button'))button.disabled=true;
  return state.pending;
 }
 function paint(){
  if(!alive())return;
  state.open=host.querySelector('details')?.open??state.open??false;
  const v=state.value,items=Array.isArray(v?.items)?v.items:[];
  host.innerHTML=`<details class="card recruiting-reminders" ${state.open?'open':''}><summary>招聘变化提醒${v?' · '+items.length+(v.more?'+':'')+' 项':''}</summary><p class="meta">岗位或资料变化后提醒核对，由你决定是否重新分析；不会自动投递或调用模型。</p>${state.error?`<p role="status">${esc(state.error)}</p>`:''}<div class="actions"><button type="button" data-reminder-refresh>刷新变化</button>${v?`<button type="button" data-reminder-toggle>${v.enabled?'暂停变化提醒':'开启变化提醒'}</button>`:''}</div><div>${items.map(item=>`<article><p class="meta">${esc(item.company||'资料更新')}</p><h3>${esc(item.title)}</h3><p>${esc(item.explanation)}</p><div class="actions">${/^[a-f0-9]{32}$/.test(item.job_id||'')?`<button type="button" data-reminder-job="${esc(item.job_id)}">查看岗位</button>`:''}${(Array.isArray(item.related_applications)?item.related_applications:[]).filter(a=>/^[a-f0-9]{32}$/.test(a.id||'')).slice(0,3).map(a=>`<button type="button" data-reminder-application="${esc(a.id)}">核对申请留档${D?' · '+esc(D.label('application',a.current_state)):''}</button>`).join('')}<button type="button" data-reminder-dismiss="${esc(item.id)}">已处理</button></div></article>`).join('')||`<p>${v?(v.enabled?'当前没有待处理变化。':'变化提醒尚未开启。'):'正在读取招聘变化…'}</p>`}</div>${v?.more?'<p class="meta">当前为最新 50 项，处理后可继续查看较早的提醒。</p>':''}</details>`;
  host.querySelector('[data-reminder-refresh]').onclick=()=>update();
  const toggle=host.querySelector('[data-reminder-toggle]');
  if(toggle)toggle.onclick=()=>update(()=>api('/api/agent/todos/settings','POST',{enabled:!v.enabled}));
  for(const button of host.querySelectorAll('[data-reminder-dismiss]'))button.onclick=()=>update(()=>api('/api/agent/todos/'+encodeURIComponent(button.dataset.reminderDismiss)+'/dismiss','POST',{}));
  for(const button of host.querySelectorAll('[data-reminder-application]'))button.onclick=()=>{if(alive())Promise.resolve(navigate('dossier',{applicationID:button.dataset.reminderApplication})).catch(e=>{if(alive()){state.error=e.message;paint();}});};
  for(const button of host.querySelectorAll('[data-reminder-job]'))button.onclick=()=>{if(alive())Promise.resolve(navigate('matching',{jobID:button.dataset.reminderJob})).catch(e=>{if(alive()){state.error=e.message;paint();}});};
  for(const button of host.querySelectorAll('button'))button.disabled=!!state.pending;
 }
 state.paint=paint;paint();if(!state.value&&!state.pending)update();
 return {refresh:()=>update(),dispose(){state.open=host.querySelector('details')?.open??state.open;disposed=true;if(state.paint===paint)delete state.paint;}};
}
root.CampusRecruitingReminders={mount};if(typeof module==='object'&&module.exports)module.exports=root.CampusRecruitingReminders;
})(typeof window==='undefined'?globalThis:window);
