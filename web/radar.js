'use strict';
const radarKinds={NEW_JOB:'新增岗位',JOB_CONTENT_CHANGED:'招聘内容变化',JOB_STATUS_CHANGED:'岗位状态变化',DEADLINE_CHANGED:'截止时间变化'};
function radarCards(rows,{disabled=false,pending=new Set()}={}){
 return rows.length?rows.map(v=>{
 const app=v.application,planned=app?.current_state==='PLANNED',rowDisabled=disabled||pending.has(v.job.id),off=rowDisabled?'disabled':'',snapshot=app?`data-app-id="${esc(app.id)}" data-app-version="${Number(app.version)||0}"`:'';
 const progress=app?`<button data-radar-application="${esc(app.id)}">${planned?'查看投递计划':'查看投递进展'}</button>`:`<button data-apply="PLANNED" data-id="${esc(v.job.id)}" ${off}>准备投递</button>`;
 return `<article class="radar-card" data-workbench-row="radar-${esc(v.job.id)}"><div class="card-top"><span>${esc(D.text(v.job.company))}</span>${pill(v.job.current_status)}</div><h3><button class="job-link" data-job="${esc(v.job.id)}">${esc(D.text(v.job.title))}</button></h3><p class="meta">${esc((v.job.locations||[]).map(D.text).join('、')||'地点待确认')}${v.ranking?.input_identity?' · '+esc(D.label('eligibility',v.eligibility)):''}${app?' · '+esc(D.label('application',app.current_state)):''}</p>${v.deadline?`<p class="deadline">截止：${esc(v.deadline_date?v.deadline_date+' 当天结束':D.date(v.deadline))}</p>`:''}${v.ranking?.input_identity?`<p>初筛参考 ${Number(v.ranking.score).toFixed(1)}</p>`:''}<div class="quick-actions"><button data-pref="${v.disposition==='SAVED'?'NONE':'SAVED'}" data-id="${esc(v.job.id)}" ${off}>${v.disposition==='SAVED'?'取消稍后看':'稍后看'}</button><button data-pref="IGNORED" data-id="${esc(v.job.id)}" ${off}>忽略</button>${progress}${!app||planned?`<button data-apply="APPLIED" data-id="${esc(v.job.id)}" ${snapshot} ${rowDisabled||app&&(!Number.isSafeInteger(app.version)||app.version<1)?'disabled':''}>已投递</button>`:''}</div></article>`;
 }).join(''):empty('当前没有需要处理的岗位。可先关注招聘来源，或完善求职资料。');
}
function radarChanges(rows){return rows.length?rows.map(c=>`<article class="change-row"><span class="change-dot" aria-hidden="true"></span><div><b>${esc(radarKinds[c.type]||'岗位变化')}</b><button class="job-link" data-job="${esc(c.job_id)}">${esc(D.text(c.title))}</button>${c.type==='JOB_STATUS_CHANGED'?`<small>${esc(D.label('job',c.from))} → ${esc(D.label('job',c.to))}</small>`:''}<small>${esc(D.date(c.created_at))}</small></div></article>`).join(''):empty('这段时间还没有岗位变化。');}
function bindRadar(box,refresh,{active=()=>true,canAct=()=>true,openJob,pending=new Set()}={}){
 for(const b of box.querySelectorAll('[data-job]'))b.onclick=()=>{if(active())return Promise.resolve(openJob?openJob(b.dataset.job):page('matching',{jobID:b.dataset.job,view:'overview'})).catch(error=>{if(active())fail(error);});};
 for(const b of box.querySelectorAll('[data-radar-application]'))b.onclick=()=>{if(active())return page('applications',{applicationID:b.dataset.radarApplication}).catch(error=>{if(active())fail(error);});};
 async function action(button,work,message){
  const id=button.dataset.id;if(!active()||!canAct()||pending.has(id))return;pending.add(id);
  const controls=[...box.querySelectorAll('[data-id]')].filter(b=>b.dataset.id===id).map(b=>[b,b.disabled]);for(const [b] of controls)b.disabled=true;
  try{await work();if(!active())return;await refresh();if(active())$('#notice').textContent=message;}
  catch(error){if(active())fail(error);}
  finally{pending.delete(id);if(active()&&canAct())for(const [b,disabled] of controls)if(b.isConnected!==false)b.disabled=disabled;}
 }
 for(const b of box.querySelectorAll('[data-pref]'))b.onclick=()=>action(b,()=>api('/api/jobs/'+b.dataset.id+'/preference','PUT',{disposition:b.dataset.pref}),b.dataset.pref==='IGNORED'?'已忽略这个岗位。':b.dataset.pref==='NONE'?'已取消稍后看。':'已加入稍后看。');
 for(const b of box.querySelectorAll('[data-apply]'))b.onclick=()=>action(b,async()=>{
  if(b.dataset.appId){const version=Number(b.dataset.appVersion);if(!Number.isSafeInteger(version)||version<1)throw new UserError('请先刷新投递状态，再记录投递。');await api('/api/applications/transition','POST',{application_id:b.dataset.appId,state:'APPLIED',version});}
  else await api('/api/applications','POST',{job_id:b.dataset.id,...(b.dataset.apply==='APPLIED'?{submitted:true}:{})});
 },b.dataset.apply==='APPLIED'?'已记录投递。':'已加入投递计划。');
}
async function radarPage(name,set,box,query,{active=()=>true,reviewInterview}={}){
 if(name==='radar'){
 if(!set(CampusUI.heading('今日待办','',`<button data-go="watches" class="btn" aria-label="关注来源">${CampusUI.icon('radar')}关注来源</button><button data-go="notifications" class="btn" aria-label="通知收件箱">${CampusUI.icon('bell')}通知收件箱</button>`)+'<div id="home-deadlines"></div><div id="today-todos">'+CampusUI.loading('正在读取待办…')+'</div><details id="radar-discoveries" class="radar-discoveries"><summary>岗位发现与变化 <span data-discovery-summary>展开查看</span></summary><div id="radar-discovery-content"></div></details>'))return;
 const deadlines=globalThis.CampusDeadlines?.mount(box.querySelector('#home-deadlines'),{api,esc,D,navigate:page,active});
 globalThis.CampusNavigation?.register({active,dispose:()=>deadlines?.dispose()});
 const todos=box.querySelector('#today-todos'),discoveries=box.querySelector('#radar-discoveries'),discoveryContent=box.querySelector('#radar-discovery-content'),summary=box.querySelector('[data-discovery-summary]');
 const bindLinks=host=>{for(const b of host.querySelectorAll('[data-go]'))b.onclick=()=>page(b.dataset.go).catch(fail);};
 bindLinks(box);
 let loadingTodos=false,loadingDiscoveries=false,digest=null;
 async function loadTodos(){
  if(!active()||loadingTodos)return;loadingTodos=true;
  const retry=todos.querySelector('[data-reload-todos]');if(retry)retry.disabled=true;
  try{const value=await api('/api/radar/todos');if(active())CampusTodos.mount(todos,value,{api,esc,D,navigate:page,reviewInterview,active,notify:message=>CampusUI.notify(message)});}
  catch(error){if(active()){todos.innerHTML=`<section class="today-panel"><h2>今日待办</h2><p class="pending-note" role="status">${esc(error.message)}</p><button data-reload-todos class="btn">重新读取待办</button></section>`;todos.querySelector('[data-reload-todos]').onclick=()=>loadTodos();}}
  finally{loadingTodos=false;}
 }
 function discoveryHTML(v){
  const metrics=[['今日新增',v.counts.new_jobs],['优先投递',v.counts.recommended_jobs],['7 天内截止',v.counts.closing_soon],['状态变化',v.counts.status_changes],['本周面试',v.counts.upcoming_interviews]];
  return `<div class="radar-metrics">${metrics.map(([label,n])=>`<div><span>${esc(label)}</span><strong>${Number(n)||0}</strong></div>`).join('')}</div><p class="meta">更新于 ${esc(D.date(v.as_of))} · 新增与变化统计过去 24 小时${v.truncated?' · 岗位分类展示前 5 条，更多请进入分类查看':''}</p><div class="section-heading"><h2>今天最值得投</h2><button data-go="jobs">查看岗位库</button></div><div class="radar-grid">${radarCards(v.recommended_jobs)}</div><div class="radar-columns"><section><div class="section-heading"><h2>最近变化</h2><button data-go="changes">过去 7 天</button></div>${radarChanges(v.recent_changes)}</section></div><h2>今日新发现</h2><div class="radar-grid">${radarCards(v.new_jobs)}</div>`;
 }
 async function loadDiscoveries(){
  if(!active()||loadingDiscoveries)return;loadingDiscoveries=true;
  discoveryContent.innerHTML=`<p class="meta" role="status">正在读取岗位动态…</p>${digest?discoveryHTML(digest):''}`;
  try{const value=await api('/api/radar/digest');if(!active())return;digest=value;summary.textContent=`新增 ${Number(value.counts.new_jobs)||0} · 7 天内截止 ${Number(value.counts.closing_soon)||0}`;discoveryContent.innerHTML='<div class="actions"><button data-refresh-discoveries class="text-btn">刷新动态</button></div>'+discoveryHTML(value);}
  catch(error){if(active()){summary.textContent='读取失败，可重试';discoveryContent.innerHTML=`<p class="pending-note" role="status">岗位动态暂不可用：${esc(error.message)}${digest?' 当前保留上次读取内容。':''}</p><div class="actions"><button data-refresh-discoveries class="btn btn-small">重新读取动态</button><button data-go="matching" class="btn btn-small">查看岗位库</button></div>${digest?discoveryHTML(digest):''}`;}}
  finally{loadingDiscoveries=false;if(active()){const refresh=discoveryContent.querySelector('[data-refresh-discoveries]');if(refresh)refresh.onclick=()=>loadDiscoveries();bindLinks(discoveryContent);bindRadar(discoveryContent,()=>loadDiscoveries(),{active});}}
 }
 discoveries.ontoggle=()=>{if(discoveries.open&&!digest&&!loadingDiscoveries)loadDiscoveries();};
 await loadTodos();return;
 }
 if(name==='changes'||name==='closing'){
 return CampusRadarBrowse.page(set,{kind:name,query,api,esc,D,navigate:page,active,cards:radarCards,changes:radarChanges,bindActions:bindRadar,notify:message=>{if(active())$('#notice').textContent=message;}});
 }
 if(name==='watches'){
 let watches=[],sources=[],catalog=[],catalogFailed=false,previewMarkup='',busy=false,bulkStop=null,drawRevision=0,loaded=false,reloadPromise=null,queuedMetadata=null,userID='',returnScroll=0,lastSource='',watchPainter=null,bindWatches=null,deferredPaint=false,deferredFull=false;
 const sourceFilters={search:'',group:'',manualOpen:false},watchFilters={search:'',status:'ALL',open:new Set()},bulkState={};
 const navigation=globalThis.CampusNavigation;
 const drafts=globalThis.CampusNavigation?.forms(document,{retainMissing:true});
 function remember(){
   if(!active()||!userID)return;
   navigation?.storeWorkflow?.(userID,'watches',{query:watchFilters.search,stage:watchFilters.status,openIDs:[...watchFilters.open],scroll:globalThis.scrollY||0,lastSource});
   navigation?.storeWorkflow?.(userID,'source_catalog',{query:sourceFilters.search,stage:sourceFilters.group,openEnded:sourceFilters.manualOpen});
   navigation?.storeWorkflow?.(userID,'source_bulk',{query:bulkState.search,stage:bulkState.filter,openEnded:bulkState.opened,listScroll:bulkState.scroll});
 }
 function resumePaint(){if(!active()||watchFilters.composing||sourceFilters.composing)return;if(deferredFull){deferredFull=deferredPaint=false;draw();}else if(deferredPaint){deferredPaint=false;watchPainter?.();}}
 function feedback(message=''){
   const host=box.querySelector?.('#watch-refresh-feedback');if(!host)return;
   host.innerHTML=message?`<p class="pending-note" role="status">${esc(message)}${loaded?' 当前保留上次读取的关注。':''}</p><button data-watch-retry class="btn btn-small">重新读取关注</button>`:'';
   const retry=host.querySelector?.('[data-watch-retry]');if(retry)retry.onclick=()=>reload();
 }
 function reload({metadata=false}={}){
   if(!active())return Promise.resolve(false);
   if(reloadPromise){if(!metadata)return reloadPromise;queuedMetadata||=reloadPromise.then(()=>reload({metadata:true})).finally(()=>queuedMetadata=null);return queuedMetadata;}
   const button=$('#refresh-watches');if(button)button.disabled=true;
   reloadPromise=(async()=>{
    try{
     if(!loaded||metadata){
      const latest=await Promise.all([api('/api/watches'),api('/api/sources'),api('/api/sources/catalog').catch(()=>null),!userID?api('/api/profile/resume/capabilities').catch(()=>null):Promise.resolve(null)]);if(!active())return false;
      if(!Array.isArray(latest[0])||!Array.isArray(latest[1]))throw Error('关注列表暂时无法读取，请重试。');
      [watches,sources,catalog]=latest;catalogFailed=!Array.isArray(catalog);catalog=catalogFailed?[]:catalog;
      if(!userID&&latest[3]?.user_id){
       userID=latest[3].user_id;const saved=navigation?.readWorkflow?.(userID,'watches')||{},directory=navigation?.readWorkflow?.(userID,'source_catalog')||{},bulk=navigation?.readWorkflow?.(userID,'source_bulk')||{};
       watchFilters.search=saved.query||'';watchFilters.status=['ALL','ENABLED','ATTENTION','PAUSED','PRIORITY'].includes(saved.stage)?saved.stage:'ALL';watchFilters.open=new Set(saved.openIDs||[]);
       sourceFilters.search=directory.query||'';sourceFilters.group=directory.stage||'';sourceFilters.manualOpen=!!directory.openEnded;
       Object.assign(bulkState,{search:bulk.query||'',filter:['ALL','FAILED','ACTIVE','DONE'].includes(bulk.stage)?bulk.stage:'ALL',opened:!!bulk.openEnded,scroll:bulk.listScroll||0});
       returnScroll=saved.scroll||0;lastSource=saved.lastSource||'';
      }
      await draw();loaded=true;
     }else{
      const latest=await api('/api/watches');if(!active()||busy)return false;if(!Array.isArray(latest))throw Error('关注列表暂时无法读取，请重试。');
      watches=latest;if(watchPainter)watchPainter();else await draw();
     }
     if(active())feedback();return true;
    }catch(error){
     if(!active())return false;
     if(!loaded)set(CampusUI.heading('招聘来源')+'<div id="watch-refresh-feedback"></div>');feedback(error.message);return false;
    }finally{if(active()){const b=$('#refresh-watches');if(b)b.disabled=false;}reloadPromise=null;}
   })();
   return reloadPromise;
 }
 async function draw(){
 if(watchFilters.composing||sourceFilters.composing){deferredFull=true;return;}
 drawRevision++;
 const supported=sources.filter(s=>s.adapter),sourceOf=id=>sources.find(s=>s.id===id);
 const minimum=s=>CampusSourceWatches.minimum(s,catalog);
 const presets=catalog.length?CampusSources.render(catalog,esc,sourceFilters):catalogFailed?'<p class="pending-note">预设入口暂时无法读取，可直接填写已支持的校招网址后预览。</p><button id="source-catalog-retry" class="btn btn-small">重新读取来源目录</button>':'';
 const directionSelect=(selected='')=>`<label>岗位方向<select name="direction"><option value="">全部方向</option>${[['rd','研发'],['algorithm','算法'],['non_tech','非技术']].map(([value,label])=>`<option value="${value}" ${selected===value?'selected':''}>${label}</option>`).join('')}</select></label>`;
 const watchForm=(v={})=>`<div class="form-grid">${v.id?`<input type="hidden" name="source_id" value="${esc(v.source_id)}">`:`<label>招聘来源<select name="source_id" required>${supported.map(s=>`<option value="${esc(s.id)}">${esc(s.name)}</option>`).join('')}</select></label>`}${input('check_interval','基础检查间隔（分钟）',(v.check_interval||3600)/60,'number',`required min="${minimum(v.id?sourceOf(v.source_id):supported[0])}" max="10080"`)}${input('keyword','标题或地点包含（可选）',v.keyword||'','text','maxlength="100"')}${v.id&&sourceOf(v.source_id)?.adapter==='xiaohongshu'?directionSelect(v.direction):''}</div><label class="check"><input type="checkbox" name="enabled" ${v.enabled===false?'':'checked'}>启用周期检查</label><label class="check"><input type="checkbox" name="adaptive" ${v.adaptive?'checked':''}>自动调整检查频率</label><label class="check"><input type="checkbox" name="priority" ${v.priority?'checked':''}>重点关注</label><button>${v.id?'保存关注设置':'开始关注'}</button>`;
 const progresses=[];
 if(!active())return;drafts?.capture();
 const watchList=CampusSourceWatches.render(watches,sources,progresses,{esc,D,form:watchForm,state:watchFilters});
 if(!set(CampusUI.heading('招聘来源')+`<div id="watch-workspace">${watchList}</div><div id="watch-refresh-feedback"></div><div id="bulk-source-import"></div>${presets}<form id="source-preview" novalidate><label>公司校招网址<input name="url" type="url" required placeholder="https://job.xiaohongshu.com/campus/position"></label><button>预览岗位源</button></form><div id="source-preview-result">${previewMarkup}</div>${supported.length?`<details><summary>使用已登记的招聘来源</summary><form id="watch-create" novalidate>${watchForm()}</form></details>`:''}`))return;
 watchPainter=()=>{
  if(!active())return;if(watchFilters.composing||sourceFilters.composing){deferredPaint=true;return;}
  const host=box.querySelector?.('#watch-workspace');if(!host){draw();return;}
  const saved=CampusUI.capture();drafts?.capture();drawRevision++;
  CampusUI.reconcile(host,CampusSourceWatches.render(watches,sources,[],{esc,D,form:watchForm,state:watchFilters}));bindWatches?.();drafts?.restore();CampusUI.restore(saved);
 };

 if(!active())return;
 bulkStop?.();bulkStop=globalThis.CampusSourceBulk?.mount(box.querySelector?.('#bulk-source-import'),{api,esc,D,active,navigate:(name,query)=>page(name,query).catch(fail),count:catalog.filter(s=>s.auto_import===true).length,state:bulkState});
 drafts?.restore();globalThis.CampusNavigation?.register({active,dirty:()=>busy||drafts?.dirty(),checkpoint:remember,dispose:()=>bulkStop?.()});
 const catalogRetry=$('#source-catalog-retry');if(catalogRetry)catalogRetry.onclick=()=>reload({metadata:true});
 const bindPresets=()=>{for(const button of box.querySelectorAll('[data-campus-preset]'))button.onclick=()=>{if(busy)return;const form=$('#source-preview'),site=catalog[Number(button.dataset.campusPreset)];if(!site||!CampusSources.ready(site))return;form.elements.url.value=site.url;form.elements.url.dispatchEvent(new Event('input',{bubbles:true}));form.elements.url.focus();};};
 bindPresets();CampusSources.mount(box.querySelector?.('#source-directory'),catalog,{esc,state:sourceFilters,bind:bindPresets,onCompositionEnd:resumePaint});
 $('#source-preview').elements.url.addEventListener('input',()=>{previewMarkup='';drafts?.forget('#source-create');$('#source-preview-result').innerHTML='';});
 formAction('#source-preview',async data=>{
   const requestedURL=String(data.get('url')).trim(),preview=await api('/api/sources/preview','POST',{url:requestedURL});if(!active()||$('#source-preview').elements.url.value.trim()!==requestedURL)return;
   drafts?.capture();drafts?.forget('#source-create');previewMarkup=`<article class="card"><h3>${esc(preview.name)} · ${Number(preview.total)} 个岗位</h3><p class="meta">${Number(preview.total)===0?'官网当前暂无该范围岗位，可以先关注，开放后自动检查。':`样例：${preview.samples.map(s=>esc(s.title)).join('、')||'暂无样例'}。岗位数据来自招聘网站，首次导入需要一些时间。`}</p><form id="source-create" novalidate><input type="hidden" name="url" value="${esc(preview.url)}"><div class="form-grid">${preview.supports_direction?directionSelect():''}${input('keyword','标题或地点包含（可选）','','text','maxlength="100"')}${input('check_interval','基础检查间隔（分钟）',360,'number',`required min="${Math.max(30,Number(preview.minimum_interval||1800)/60)}" max="10080"`)}</div><label class="check"><input type="checkbox" name="enabled" checked>启用周期检查</label><label class="check"><input type="checkbox" name="adaptive" checked>自动调整检查频率</label><label class="check"><input type="checkbox" name="priority">重点关注</label><button>开始关注并导入</button></form></article>`;
   await draw();
 });
 if(previewMarkup)formAction('#source-create',async values=>{
   if(busy)return;busy=true;const point=drafts?.savepoint('#source-create'),urlPoint=drafts?.savepoint('#source-preview');
   try{const result=await api('/api/sources/from-url','POST',{url:values.get('url'),direction:values.get('direction')||'',keyword:values.get('keyword'),check_interval:Number(values.get('check_interval'))*60,enabled:values.has('enabled'),adaptive:values.has('adaptive'),priority:values.has('priority')});if(!active())return;
     drafts?.saved('#source-create',point);drafts?.saved('#source-preview',urlPoint);
     await refreshAfterSave(result.existing?'此前已关注这个招聘项目。':'已创建关注。岗位将在后台逐步导入。');
   }finally{busy=false;}
 });
 const save=async(data,id)=>{
   if(busy)return;busy=true;const selector=id?'#watch-'+id:'#watch-create',point=drafts?.savepoint(selector);
   try{await api('/api/watches'+(id?'/'+id:''),id?'PUT':'POST',{source_id:data.get('source_id'),check_interval:Number(data.get('check_interval'))*60,keyword:data.get('keyword'),direction:data.get('direction')||'',enabled:data.has('enabled'),adaptive:data.has('adaptive'),priority:data.has('priority')});if(!active())return;
     drafts?.saved(selector,point);await refreshAfterSave(id?'关注设置已保存。':'已创建关注。');
   }finally{busy=false;}
 };
 if(supported.length){formAction('#watch-create',data=>save(data));const form=$('#watch-create');form.elements.source_id.onchange=()=>{form.elements.check_interval.min=String(minimum(sourceOf(form.elements.source_id.value)));};}
 bindWatches=()=>{
 const revision=drawRevision;
 CampusSourceWatches.mount(box.querySelector?.('#source-watches'),watches,sources,watchFilters,{onCompositionEnd:resumePaint});
 const refresh=$('#refresh-watches');if(refresh)refresh.onclick=()=>{if(!busy)return reload();};
 for(const v of watches)formAction('#watch-'+v.id,data=>save(data,v.id));
 for(const b of box.querySelectorAll('[data-delete-watch]'))b.onclick=async()=>{
   if(busy)return;busy=true;
   try{await api('/api/watches/'+b.dataset.deleteWatch,'DELETE');if(!active())return;drafts?.forget('#watch-'+b.dataset.deleteWatch);await refreshAfterSave('已删除关注。');}catch(e){fail(e);}finally{busy=false;}
 };
 for(const b of box.querySelectorAll('[data-source-jobs]'))b.onclick=()=>{lastSource=b.dataset.sourceJobs;return page('source_jobs',{sourceID:b.dataset.sourceJobs,page:1}).catch(fail);};
 // Detail progress must not delay the first usable render or replace edited forms.
 for(const watch of watches.filter(v=>v.last_outcome==='PROCESSING'))api('/api/watches/'+encodeURIComponent(watch.id)+'/progress').then(progress=>{if(active()&&revision===drawRevision)CampusSourceWatches.progress(box.querySelector?.('#source-watches'),watch.id,progress);}).catch(()=>{});
 };
 bindWatches();
 }
 async function refreshAfterSave(message){
   if(!await reload({metadata:true}))message+=' 刷新进度失败，请稍后点击“刷新进度”。';
   if(active())$('#notice').textContent=message;
 }
 await reload();if(active()&&loaded&&query?.resume){globalThis.scrollTo?.({top:returnScroll,behavior:'instant'});[...box.querySelectorAll('[data-source-jobs]')].find(b=>b.dataset.sourceJobs===(query.sourceID||lastSource))?.focus?.({preventScroll:true});}return;
 }
 if(name==='source_jobs'){
 const sourceID=query?.sourceID,pageNumber=Number(query?.page)||1;
 if(!sourceID){set(empty('请从关注源进入来源岗位列表。'));return;}
 const result=await api('/api/sources/'+encodeURIComponent(sourceID)+'/jobs?page='+pageNumber);
 if(!set(`<h2>来源岗位</h2><p>已导入 ${result.total} 个岗位；导入仍在进行时，数字会继续增加。</p><div class="actions"><button data-go-watches>返回关注源</button><button data-refresh-source>刷新</button></div>${result.jobs.length?`<div class="table-wrap"><table><thead><tr><th>岗位</th><th>公司</th><th>地点</th><th>状态</th></tr></thead><tbody>${result.jobs.map(j=>`<tr><td><button data-job="${esc(j.id)}">${esc(D.text(j.title))}</button></td><td>${esc(D.text(j.company))}</td><td>${esc((j.locations||[]).map(D.text).join('、')||'地点待确认')}</td><td>${pill(j.current_status)}</td></tr>`).join('')}</tbody></table></div>`:empty('尚未导入岗位。请稍后刷新，或查看关注源的检查状态。')}<div class="actions"><button data-page-source="${pageNumber-1}" ${pageNumber<=1?'disabled':''}>上一页</button><span>第 ${pageNumber} 页</span><button data-page-source="${pageNumber+1}" ${pageNumber*result.page_size>=result.total?'disabled':''}>下一页</button></div>`))return;
 box.querySelector('[data-go-watches]').onclick=()=>page('watches',{resume:true,sourceID}).catch(fail);
 box.querySelector('[data-refresh-source]').onclick=()=>page('source_jobs',{sourceID,page:pageNumber,resume:true,scroll:globalThis.scrollY||0}).catch(fail);
 for(const b of box.querySelectorAll('[data-page-source]'))b.onclick=()=>page('source_jobs',{sourceID,page:Number(b.dataset.pageSource)}).catch(fail);
 for(const b of box.querySelectorAll('[data-job]'))b.onclick=()=>page('matching',{jobID:b.dataset.job,view:'overview',returnTo:{page:'source_jobs',sourceID,sourcePage:pageNumber,scroll:globalThis.scrollY||0,lastJob:b.dataset.job}}).catch(fail);
 if(query?.resume){globalThis.scrollTo?.({top:Number.isFinite(query.scroll)?Math.max(0,Math.min(query.scroll,1e7)):0,behavior:'instant'});[...box.querySelectorAll('[data-job]')].find(b=>b.dataset.job===query.lastJob)?.focus?.({preventScroll:true});}
 return;
 }
 if(name==='notifications'){
 return CampusNotifications.page(set,{api,esc,D,navigate:page,query,active,notify:message=>{if(active())CampusUI.notify(message);}});
 }
}
