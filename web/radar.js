'use strict';
const radarKinds={NEW_JOB:'新增岗位',JOB_CONTENT_CHANGED:'招聘内容变化',JOB_STATUS_CHANGED:'岗位状态变化',DEADLINE_CHANGED:'截止时间变化'};
function radarCards(rows){return rows.length?rows.map(v=>`<article class="radar-card"><div class="card-top"><span>${esc(D.text(v.job.company))}</span>${pill(v.job.current_status)}</div><h3><button class="job-link" data-job="${esc(v.job.id)}">${esc(D.text(v.job.title))}</button></h3><p class="meta">${esc((v.job.locations||[]).map(D.text).join('、')||'地点待确认')} · ${esc(D.label('eligibility',v.eligibility))}</p>${v.deadline?`<p class="deadline">截止：${esc(D.date(v.deadline))}</p>`:''}${v.ranking?.input_identity?`<p>优先级 ${Number(v.ranking.score).toFixed(1)} <span class="meta">· ${v.disposition==='SAVED'?'已收藏 · ':''}依据详见岗位评分</span></p>`:''}<div class="quick-actions"><button data-pref="SAVED" data-id="${esc(v.job.id)}">稍后看</button><button data-pref="IGNORED" data-id="${esc(v.job.id)}">忽略</button><button data-apply="PLANNED" data-id="${esc(v.job.id)}">准备投递</button><button data-apply="APPLIED" data-id="${esc(v.job.id)}">已投递</button></div></article>`).join(''):empty('当前没有需要处理的岗位。可先关注招聘来源，或完善求职资料。');}
function radarChanges(rows){return rows.length?rows.map(c=>`<article class="change-row"><span class="change-dot" aria-hidden="true"></span><div><b>${esc(radarKinds[c.type]||'岗位变化')}</b><button class="job-link" data-job="${esc(c.job_id)}">${esc(D.text(c.title))}</button>${c.type==='JOB_STATUS_CHANGED'?`<small>${esc(D.label('job',c.from))} → ${esc(D.label('job',c.to))}</small>`:''}<small>${esc(D.date(c.created_at))}</small></div></article>`).join(''):empty('这段时间还没有岗位变化。');}
function bindRadar(box,refresh){
 for(const b of box.querySelectorAll('[data-job]'))b.onclick=()=>detail(b.dataset.job).catch(fail);
 for(const b of box.querySelectorAll('[data-pref]'))b.onclick=async()=>{b.disabled=true;try{await api('/api/jobs/'+b.dataset.id+'/preference','PUT',{disposition:b.dataset.pref});await refresh();}catch(e){fail(e);b.disabled=false;}};
 for(const b of box.querySelectorAll('[data-apply]'))b.onclick=async()=>{b.disabled=true;try{
 const apps=await api('/api/applications');let app=apps.find(a=>a.job_id===b.dataset.id);
 if(!app)app=await api('/api/applications','POST',{job_id:b.dataset.id,...(b.dataset.apply==='APPLIED'?{submitted:true}:{})});
 if(b.dataset.apply==='PLANNED'&&app.current_state!=='PLANNED')throw new UserError('当前已有投递进展，请在投递进展中查看；结束的投递不能重新开启。');
 if(b.dataset.apply==='APPLIED'&&app.current_state==='PLANNED')await api('/api/applications/transition','POST',{application_id:app.id,state:'APPLIED',version:app.version});
 else if(b.dataset.apply==='APPLIED'&&app.current_state!=='APPLIED')throw new UserError('当前投递已进入后续阶段，请在投递进展中查看。');
 await refresh();$('#notice').textContent=b.dataset.apply==='APPLIED'?'已记录投递。':'已加入投递计划。';
 }catch(e){fail(e);b.disabled=false;}};
}
async function radarPage(name,set,box,query,{active=()=>true,reviewInterview}={}){
 if(name==='radar'){
 const [todoResult,digestResult]=await Promise.allSettled([api('/api/radar/todos'),api('/api/radar/digest')]);if(!active())return;
 const todoHTML=todoResult.status==='fulfilled'?'<div id="today-todos"></div>':`<section class="today-panel"><h2>今日待办</h2><p class="pending-note">${esc(todoResult.reason.message)}</p><button data-reload-radar class="btn">重新读取</button></section>`;
 let discoveries='';
 if(digestResult.status==='fulfilled'){
  const v=digestResult.value,metrics=[['今日新增',v.counts.new_jobs],['优先投递',v.counts.recommended_jobs],['7 天内截止',v.counts.closing_soon],['状态变化',v.counts.status_changes],['本周面试',v.counts.upcoming_interviews]];
  discoveries=`<details class="radar-discoveries"><summary>岗位发现与变化 <span>新增 ${v.counts.new_jobs} · 7 天内截止 ${v.counts.closing_soon}</span></summary><div class="radar-metrics">${metrics.map(([label,n])=>`<div><span>${esc(label)}</span><strong>${n}</strong></div>`).join('')}</div><p class="meta">更新于 ${esc(D.date(v.as_of))} · 新增与变化统计过去 24 小时${v.truncated?' · 岗位分类展示前 5 条，更多请进入分类查看':''}</p><div class="section-heading"><h2>今天最值得投</h2><button data-go="jobs">查看岗位库</button></div><div class="radar-grid">${radarCards(v.recommended_jobs)}</div><div class="radar-columns"><section><div class="section-heading"><h2>最近变化</h2><button data-go="changes">过去 7 天</button></div>${radarChanges(v.recent_changes)}</section><section><div class="section-heading"><h2>即将截止</h2><button data-go="closing">查看截止雷达</button></div>${radarCards(v.closing_soon)}</section></div><h2>今日新发现</h2><div class="radar-grid">${radarCards(v.new_jobs)}</div></details>`;
 }else discoveries=`<section class="note-box"><p>岗位动态暂不可用：${esc(digestResult.reason.message)}</p><button data-go="matching" class="btn btn-small">查看岗位库</button></section>`;
 if(!set(CampusUI.heading('今日待办','',`<button data-go="watches" class="btn" aria-label="关注来源">${CampusUI.icon('radar')}关注来源</button><button data-go="notifications" class="btn" aria-label="通知收件箱">${CampusUI.icon('bell')}通知收件箱</button>`)+todoHTML+discoveries))return;
 if(todoResult.status==='fulfilled')CampusTodos.mount(box.querySelector('#today-todos'),todoResult.value,{api,esc,D,navigate:page,reviewInterview,active,notify:message=>CampusUI.notify(message)});
 for(const b of box.querySelectorAll('[data-go]'))b.onclick=()=>page(b.dataset.go).catch(fail);
 for(const b of box.querySelectorAll('[data-reload-radar]'))b.onclick=()=>page('radar').catch(fail);
 bindRadar(box,()=>page('radar'));return;
 }
 if(name==='changes'||name==='closing'){
 const days=Number(query)||(name==='changes'?7:7),values=name==='changes'?[1,7]:[3,7,14];const rows=await api('/api/radar/'+name+'?days='+days);
 if(!set(`<h2>${name==='changes'?'最近变化':'截止雷达'}</h2><div class="actions">${values.map(n=>`<button data-days="${n}" aria-pressed="${days===n}">${name==='changes'?'过去':'未来'} ${n} 天</button>`).join('')}</div>${name==='changes'?'<p class="meta">按发生时间展示最近 100 条变化。</p>'+radarChanges(rows):'<div class="radar-grid">'+radarCards(rows)+'</div>'}`))return;
 for(const b of box.querySelectorAll('[data-days]'))b.onclick=()=>page(name,b.dataset.days).catch(fail);bindRadar(box,()=>page(name,days));return;
 }
 if(name==='watches'){
 let watches=[],sources=[],catalog=[],catalogFailed=false,previewMarkup='',busy=false,bulkStop=null;
 const sourceFilters={search:'',group:'',manualOpen:false};
 const drafts=globalThis.CampusNavigation?.forms(document,{retainMissing:true});
 async function reload(){
   drafts?.capture();const latest=await Promise.all([api('/api/watches'),api('/api/sources'),api('/api/sources/catalog').catch(()=>null)]);if(!active())return;
   [watches,sources,catalog]=latest;catalogFailed=catalog===null;catalog=catalog||[];await draw();
 }
 async function draw(){
 const supported=sources.filter(s=>s.adapter),sourceOf=id=>sources.find(s=>s.id===id);
 const minimum=s=>['xiaohongshu','baidu','meituan','jd','netease','alibaba','bilibili','kuaishou','oppo','siemens','haier','lenovo','midea','byd','hikvision','qihoo360','sany','inovance','vivo','honor','sgm','ctrip','tencent','sap','ths','cmbnt','netease_game','leihuo','ctyun','ctcloud','mihoyo','pingan_tech','pingan_oneconnect','pingan_wallet','cmcloud','cmiot','cmhome','gbits','hundsun','yuewen','tcl_digital','tcl_honghu','cec_software'].includes(s?.adapter)?30:5;
 const presets=catalog.length?CampusSources.render(catalog,esc,sourceFilters):catalogFailed?'<p class="pending-note">预设入口暂时无法读取，可直接填写已支持的校招网址后预览。</p>':'';
 const directionSelect=(selected='')=>`<label>岗位方向<select name="direction"><option value="">全部方向</option>${[['rd','研发'],['algorithm','算法'],['non_tech','非技术']].map(([value,label])=>`<option value="${value}" ${selected===value?'selected':''}>${label}</option>`).join('')}</select></label>`;
 const watchForm=(v={})=>`<div class="form-grid">${v.id?`<input type="hidden" name="source_id" value="${esc(v.source_id)}">`:`<label>招聘来源<select name="source_id" required>${supported.map(s=>`<option value="${esc(s.id)}">${esc(s.name)} · ${esc(s.adapter)}</option>`).join('')}</select></label>`}${input('check_interval','基础检查间隔（分钟）',(v.check_interval||3600)/60,'number',`required min="${minimum(v.id?sourceOf(v.source_id):supported[0])}" max="10080"`)}${input('keyword','标题或地点包含（可选）',v.keyword||'','text','maxlength="100"')}${v.id&&sourceOf(v.source_id)?.adapter==='xiaohongshu'?directionSelect(v.direction):''}</div><label class="check"><input type="checkbox" name="enabled" ${v.enabled===false?'':'checked'}>启用周期检查</label><label class="check"><input type="checkbox" name="adaptive" ${v.adaptive?'checked':''}>自动调整检查频率（无变化放缓，有变化加快）</label><label class="check"><input type="checkbox" name="priority" ${v.priority?'checked':''}>重点关注（自动调整时优先检查）</label><button>${v.id?'保存关注设置':'开始关注'}</button>`;
 const scheduleReasons={FIXED:'固定间隔',BASE:'基础间隔',PRIORITY:'重点关注或临近截止',RECENT_CHANGE:'近期有变化',UNCHANGED_BACKOFF:'连续无变化，降低频率',FAILURE_BACKOFF:'连续失败，等待恢复'};
 const outcomes={RESTORED_PAUSED:'恢复副本中的关注已暂停，请核对后重新启用',QUEUED:'等待后台检查',PENDING:'等待检查',PROCESSING:'正在核验岗位',SUCCESS:'检查完成',PARTIAL_FAILURE:'部分岗位获取失败',DISCOVERY_FAILED:'来源暂时无法读取',FAILED:'本次检查失败'};
 const progresses=await Promise.all(watches.map(v=>v.last_outcome==='PROCESSING'?api('/api/watches/'+encodeURIComponent(v.id)+'/progress').catch(()=>null):Promise.resolve(null)));
 if(!active())return;drafts?.capture();
 if(!set(`<h2>关注源</h2><p>选择公司或填写校招网址，预览后开始导入。</p><div id="bulk-source-import"></div>${presets}<form id="source-preview" novalidate><label>公司校招网址<input name="url" type="url" required placeholder="https://job.xiaohongshu.com/campus/position"></label><button>预览岗位源</button></form><div id="source-preview-result">${previewMarkup}</div>${supported.length?`<details><summary>使用已登记的招聘来源</summary><form id="watch-create" novalidate>${watchForm()}</form></details>`:''}<div class="section-heading"><h3>我的关注</h3><button id="refresh-watches">刷新进度</button></div>${watches.length?watches.map((v,i)=>`<article class="card"><h3>${esc(sourceOf(v.source_id)?.name||v.source_id)} <span class="meta">${v.enabled?'已启用':'已暂停'}</span></h3><p>${esc(outcomes[v.last_outcome]||'状态待确认')}${progresses[i]?.expected?` · 已处理 ${progresses[i].completed}/${progresses[i].expected}${progresses[i].failed?`，失败 ${progresses[i].failed}`:''}`:''}</p><p class="meta">上次检查：${v.last_checked_at?esc(D.date(v.last_checked_at)):'尚未检查'} · 下次检查：${v.enabled?esc(D.date(v.next_check_at)):'已暂停'}${v.effective_interval?` · 当前间隔 ${Math.round(v.effective_interval/60)} 分钟（${esc(scheduleReasons[v.schedule_reason]||'等待检查')}）`:''}</p><div class="actions"><button data-source-jobs="${esc(v.source_id)}">查看已导入岗位</button><button data-delete-watch="${esc(v.id)}">删除关注</button></div><form id="watch-${esc(v.id)}" novalidate>${watchForm(v)}</form></article>`).join(''):empty('还没有关注来源。先输入公司校招网址，预览后即可开始关注。')}`))return;

 if(!active())return;
 bulkStop?.();bulkStop=globalThis.CampusSourceBulk?.mount(box.querySelector?.('#bulk-source-import'),{api,esc,D,active,navigate:(name,query)=>page(name,query).catch(fail),count:catalog.filter(s=>s.auto_import===true).length});
 drafts?.restore();globalThis.CampusNavigation?.register({active,dirty:()=>busy||drafts?.dirty()});
 $('#refresh-watches').onclick=()=>{if(!busy)reload().catch(fail);};
 const bindPresets=()=>{for(const button of box.querySelectorAll('[data-campus-preset]'))button.onclick=()=>{if(busy)return;const form=$('#source-preview'),site=catalog[Number(button.dataset.campusPreset)];if(!site||!CampusSources.ready(site))return;form.elements.url.value=site.url;form.elements.url.dispatchEvent(new Event('input',{bubbles:true}));form.elements.url.focus();};};
 bindPresets();CampusSources.mount(box.querySelector?.('#source-directory'),catalog,{esc,state:sourceFilters,bind:bindPresets});
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
 for(const v of watches)formAction('#watch-'+v.id,data=>save(data,v.id));
 for(const b of box.querySelectorAll('[data-delete-watch]'))b.onclick=async()=>{
   if(busy)return;busy=true;
   try{await api('/api/watches/'+b.dataset.deleteWatch,'DELETE');if(!active())return;drafts?.forget('#watch-'+b.dataset.deleteWatch);await refreshAfterSave('已删除关注。');}catch(e){fail(e);}finally{busy=false;}
 };
 for(const b of box.querySelectorAll('[data-source-jobs]'))b.onclick=()=>page('source_jobs',{sourceID:b.dataset.sourceJobs,page:1}).catch(fail);
 }
 async function refreshAfterSave(message){
   try{await reload();}catch{message+=' 刷新进度失败，请稍后点击“刷新进度”。';}
   if(active())$('#notice').textContent=message;
 }
 await reload();return;
 }
 if(name==='source_jobs'){
 const sourceID=query?.sourceID,pageNumber=Number(query?.page)||1;
 if(!sourceID){set(empty('请从关注源进入来源岗位列表。'));return;}
 const result=await api('/api/sources/'+encodeURIComponent(sourceID)+'/jobs?page='+pageNumber);
 if(!set(`<h2>来源岗位</h2><p>已导入 ${result.total} 个岗位；导入仍在进行时，数字会继续增加。</p><div class="actions"><button data-go-watches>返回关注源</button><button data-refresh-source>刷新</button></div>${result.jobs.length?`<div class="table-wrap"><table><thead><tr><th>岗位</th><th>公司</th><th>地点</th><th>状态</th></tr></thead><tbody>${result.jobs.map(j=>`<tr><td><button data-job="${esc(j.id)}">${esc(D.text(j.title))}</button></td><td>${esc(D.text(j.company))}</td><td>${esc((j.locations||[]).map(D.text).join('、')||'地点待确认')}</td><td>${pill(j.current_status)}</td></tr>`).join('')}</tbody></table></div>`:empty('尚未导入岗位。请稍后刷新，或查看关注源的检查状态。')}<div class="actions"><button data-page-source="${pageNumber-1}" ${pageNumber<=1?'disabled':''}>上一页</button><span>第 ${pageNumber} 页</span><button data-page-source="${pageNumber+1}" ${pageNumber*result.page_size>=result.total?'disabled':''}>下一页</button></div>`))return;
 box.querySelector('[data-go-watches]').onclick=()=>page('watches').catch(fail);
 box.querySelector('[data-refresh-source]').onclick=()=>page('source_jobs',{sourceID,page:pageNumber}).catch(fail);
 for(const b of box.querySelectorAll('[data-page-source]'))b.onclick=()=>page('source_jobs',{sourceID,page:Number(b.dataset.pageSource)}).catch(fail);
 for(const b of box.querySelectorAll('[data-job]'))b.onclick=()=>detail(b.dataset.job).catch(fail);
 return;
 }
 if(name==='notifications'){
 const rows=await api('/api/notifications');if(!set(`<h2>通知收件箱</h2><button id="refresh-inbox">更新提醒</button>${rows.length?rows.map(n=>`<article class="card ${n.read_at?'read-notification':''}"><h3>${esc(n.title)} ${n.read_at?'':'<span class="unread">未读</span>'}</h3><p>${esc(n.body)}</p><small>${esc(D.date(n.created_at))}</small><div class="actions">${n.entity_type==='JOB'?`<button data-job="${esc(n.entity_id)}">查看岗位</button>`:''}${!n.read_at?`<button data-read="${esc(n.id)}">标为已读</button>`:''}</div></article>`).join(''):empty('暂时没有通知。关注来源后，有值得处理的变化会显示在这里。')}`))return;
 $('#refresh-inbox').onclick=async()=>{try{await api('/api/notifications/refresh','POST',{});await page('notifications');}catch(e){fail(e);}};for(const b of box.querySelectorAll('[data-read]'))b.onclick=async()=>{try{await api('/api/notifications/'+b.dataset.read+'/read','POST',{});await page('notifications');}catch(e){fail(e);}};bindRadar(box,()=>page('notifications'));return;
 }
 if(name==='preferences'){
 const rows=await api('/api/preferences');if(!set(`<h2>稍后看与忽略</h2>${rows.filter(v=>v.disposition!=='NONE').map(v=>`<article class="card"><button data-job="${esc(v.job_id)}">查看岗位</button><span>${v.disposition==='SAVED'?'稍后看':'已忽略'}</span><button data-pref="NONE" data-id="${esc(v.job_id)}">恢复默认</button></article>`).join('')||empty('还没有收藏或忽略的岗位。')}`))return;bindRadar(box,()=>page('preferences'));
 }
}
