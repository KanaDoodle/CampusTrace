'use strict';
const CampusTodos=(function(root){
 const labels={PLANNED_CLOSING:'计划即将截止',INTERVIEW_UPCOMING:'本周面试',REVIEW_PENDING:'待复盘',ANALYSIS_FAILED:'分析失败'};
 function target(item){
  if(item.kind==='PLANNED_CLOSING')return ['applications',{applicationID:item.application_id}];
  if(item.kind==='INTERVIEW_UPCOMING')return ['interviews',{interviewID:item.interview_id}];
  if(item.kind==='REVIEW_PENDING')return ['review',item.interview_id];
  if(item.kind==='ANALYSIS_FAILED')return ['matching',{taskID:item.task_id}];
  return null;
 }
 function render(v,{esc,D},kind='',expanded=false){
  const counts=v.counts||{},total=Object.values(counts).reduce((sum,n)=>sum+Number(n||0),0),items=(v.items||[]).filter(item=>!kind||item.kind===kind),shown=expanded?items:items.slice(0,6);
  const action={PLANNED_CLOSING:'查看投递计划',INTERVIEW_UPCOMING:'查看面试安排',REVIEW_PENDING:'填写本轮复盘',ANALYSIS_FAILED:'查看失败任务'};
  const date={PLANNED_CLOSING:'截止',INTERVIEW_UPCOMING:'面试时间',REVIEW_PENDING:'记录完成',ANALYSIS_FAILED:'失败任务更新'};
  return `<section class="today-panel" aria-label="今日待办"><div class="today-heading"><div><h2>今日待办 <span>${total} 项</span></h2></div><button id="todo-refresh" class="btn btn-subtle btn-small">刷新待办</button></div><div class="todo-filters" aria-label="待办分类"><button data-todo-kind="" aria-pressed="${kind===''}">全部 <span>${total}</span></button>${Object.entries(labels).map(([key,label])=>`<button data-todo-kind="${key}" aria-pressed="${kind===key}">${label} <span>${Number(counts[key]||0)}</span></button>`).join('')}</div><div class="todo-list">${shown.length?shown.map(item=>`<article class="todo-row"><span class="todo-kind ${item.kind==='PLANNED_CLOSING'?'is-urgent':''}">${labels[item.kind]||'待处理'}</span><div class="todo-context"><p>${esc(item.job?.company||'公司信息暂不可用')}${item.round?' · 第 '+Number(item.round)+' 轮':''}</p><h3>${esc(item.job?.title||'岗位信息暂不可用')}</h3><span>${date[item.kind]}：${esc(D.date(item.at))}${item.job?.locations?.length?' · '+esc(item.job.locations.join(' / ')):''}</span></div><button class="btn btn-small" data-todo-open="${esc(item.id)}">${action[item.kind]||'查看记录'}</button></article>`).join(''):`<div class="todo-empty"><p>${kind?'这类待办已处理完。':'目前没有需要处理的待办。'}</p><span>可继续查看岗位库；新安排和未处理事项会在这里汇总。</span></div>`}</div>${!expanded&&items.length>shown.length?'<button id="todo-expand" class="btn btn-subtle btn-small">展开更多待办</button>':''}${Object.entries(v.truncated||{}).some(([key,yes])=>yes&&(!kind||kind===key))?'<p class="form-note todo-limit">每类列出前 5 项，全部数量见上方分类；更多记录可进入对应页面查看。</p>':''}<div class="todo-footer"><span>更新于 ${esc(D.date(v.as_of))} · 查看待办不调用模型</span><div><button class="text-btn" data-todo-page="applications">投递进展</button><button class="text-btn" data-todo-page="interviews">面试与复盘</button><button class="text-btn" data-todo-page="matching">岗位库</button></div></div></section>`;
 }
 function mount(box,initial,{api,esc,D,navigate,reviewInterview,active=()=>true,notify}){
  let snapshot=initial,kind='',expanded=false,busy=false;
  const go=(...args)=>Promise.resolve(navigate(...args)).catch(error=>notify(error.message));
  function draw(){
   if(!active()||!box.isConnected)return;
   box.innerHTML=render(snapshot,{esc,D},kind,expanded);
   for(const button of box.querySelectorAll('[data-todo-kind]'))button.onclick=()=>{kind=button.dataset.todoKind;expanded=false;draw();};
   for(const button of box.querySelectorAll('[data-todo-page]'))button.onclick=()=>go(button.dataset.todoPage);
   for(const button of box.querySelectorAll('[data-todo-open]'))button.onclick=()=>{const item=snapshot.items.find(item=>item.id===button.dataset.todoOpen),next=item&&target(item);if(!next)return;if(next[0]==='review')Promise.resolve(reviewInterview(next[1])).catch(error=>notify(error.message));else go(...next);};
   box.querySelector('#todo-expand')?.addEventListener('click',()=>{expanded=true;draw();});
   const refresh=box.querySelector('#todo-refresh');refresh.disabled=busy;
   refresh.onclick=async()=>{if(busy)return;busy=true;refresh.disabled=true;try{const latest=await api('/api/radar/todos');if(!active())return;snapshot=latest;notify('待办已刷新。');}catch(error){if(active())notify(error.message);}finally{busy=false;draw();}};
  }
  draw();
 }
 const api={labels,target,render,mount};if(typeof module==='object'&&module.exports)module.exports=api;root.CampusTodos=api;return api;
})(typeof window==='undefined'?globalThis:window);
