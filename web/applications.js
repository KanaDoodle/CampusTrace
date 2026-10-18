'use strict';
const CampusApplications=(function(root){
  function officialLink(value){try{const u=new URL(value);return ['http:','https:'].includes(u.protocol)&&!u.username&&!u.password?u.href:'';}catch{return '';}}
  function hasFollowUp(row){return !ended(row)&&Number.isFinite(Date.parse(row.follow_up_at));}
  function beijingDayEnd(now=Date.now()){const d=new Date(now+8*3600000);return Date.UTC(d.getUTCFullYear(),d.getUTCMonth(),d.getUTCDate()+1)-8*3600000;}
  function dueToday(row,now=Date.now()){return hasFollowUp(row)&&Date.parse(row.follow_up_at)<beijingDayEnd(now);}
  function followUpInput(value){
    const at=new Date(value);if(!value||!Number.isFinite(at.getTime()))return '';
    const d=new Date(at.getTime()+8*3600000),pad=n=>String(n).padStart(2,'0');
    return `${d.getUTCFullYear()}-${pad(d.getUTCMonth()+1)}-${pad(d.getUTCDate())}T${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
  }
  function followUpTime(value){
    if(typeof value!=='string'||!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value))return null;
    const at=new Date(value+':00+08:00'),year=Number(value.slice(0,4));return Number.isFinite(at.getTime())&&year>=2000&&year<=2100&&followUpInput(at.toISOString())===value?at.toISOString():null;
  }
  function followUpShortcut(days,now=Date.now()){
    const d=new Date(now+8*3600000);return followUpInput(new Date(Date.UTC(d.getUTCFullYear(),d.getUTCMonth(),d.getUTCDate()+days,2)).toISOString());
  }
  function followUpCard(a,{esc,D,now}){
    if(!hasFollowUp(a))return '';
    return `<div class="application-follow-up ${Date.parse(a.follow_up_at)<=now?'is-due':''}"><div><span>${Date.parse(a.follow_up_at)<=now?'已到跟进时间':dueToday(a,now)?'今天需跟进':'待跟进'}</span><strong>${esc(a.next_action||'跟进投递进展')}</strong><small>${esc(D.date(a.follow_up_at))}</small></div><button type="button" class="btn btn-small" data-application-follow-up-complete="${esc(a.id)}">完成跟进</button></div>`;
  }
  function followUpEditor(a,{esc}){
    if(ended(a))return '';
    return `<details data-application-editor="${esc(a.id)}:follow"><summary>${hasFollowUp(a)?'调整跟进提醒':'设置跟进提醒'}</summary><form id="application-follow-up-${esc(a.id)}" class="application-follow-up-form"><input type="hidden" name="record_version" value="${Number(a.version)||0}"><label>跟进时间（北京时间）<input type="datetime-local" name="follow_up_at" required min="2000-01-01T00:00" max="2100-12-31T23:59" value="${esc(followUpInput(a.follow_up_at))}"></label><div class="actions follow-up-shortcuts">${[[1,'明天'],[3,'3 天后'],[7,'1 周后']].map(([days,label])=>`<button type="button" class="btn btn-small" data-follow-up-days="${days}">${label}</button>`).join('')}</div><label>要做的事（可选）<input name="next_action" maxlength="200" value="${esc(a.next_action||'')}" placeholder="例如：完成网申、查看笔试通知"></label><div class="actions"><button class="btn">保存提醒</button>${hasFollowUp(a)?`<button type="button" class="btn btn-subtle" data-application-follow-up-cancel="${esc(a.id)}">取消提醒</button>`:''}</div></form></details>`;
  }
  function render(rows,{esc,D,now=Date.now()}){
    if(!rows.length)return '<p class="empty">还没有投递记录。可在岗位详情中将心仪岗位加入投递计划。</p>';
    return `<div class="application-list">${rows.map(a=>{const j=a.job,url=officialLink(a.official_url),states=a.next_states||[];return `<article class="application-record" data-application-id="${esc(a.id)}"><div class="application-head"><div><p class="application-company">${esc(j?.company||'公司信息暂不可用')}</p><h3><button class="text-btn application-title" data-application-job="${esc(a.job_id)}">${esc(j?.title||'查看岗位记录')}</button></h3><p class="meta">${esc((j?.locations||[]).join(' / '))}${j?.job_type?' · '+esc(D.label('job_type',j.job_type)):''}</p></div><span class="pill">${esc(D.label('application',a.current_state))}</span></div><dl class="application-info"><div><dt>投递时间</dt><dd>${a.applied_at?esc(D.date(a.applied_at)):'尚未记录投递'}</dd></div><div><dt>所用简历版本</dt><dd>${esc(a.resume_version||'尚未记录')}</dd></div><div><dt>最近更新</dt><dd>${esc(D.date(a.updated_at))}</dd></div></dl>${a.note?`<p class="application-note">${esc(a.note)}</p>`:''}${followUpCard(a,{esc,D,now})}<div class="actions">${url?`<a class="btn btn-small" href="${esc(url)}" target="_blank" rel="noopener noreferrer">查看招聘官网</a>`:''}<button class="btn btn-small" data-application-preparation="${esc(a.job_id)}">岗位准备清单</button><button class="btn btn-small" data-application-interview="${esc(a.id)}">记录面试安排</button><button class="btn btn-small" data-application-history="${esc(a.id)}">查看进展记录</button></div><details data-application-editor="${esc(a.id)}"><summary>更新进展与投递资料</summary><div class="application-edit">${states.length?`<form id="application-stage-${esc(a.id)}"><h4>更新进展</h4><label>下一阶段<select name="state">${states.map(v=>`<option value="${esc(v)}">${esc(D.label('application',v))}</option>`).join('')}</select></label><label>本次进展说明（可选）<textarea name="note" maxlength="600" placeholder="例如：完成笔试，等待面试通知"></textarea></label><button class="btn">保存进展</button></form>`:`<p class="meta">${ended(a)?'这条投递已结束，仍可补充资料和查看历史记录。':'可选的下一阶段尚未读取，请刷新记录后再更新进展。'}</p>`}<form id="application-details-${esc(a.id)}"><input type="hidden" name="record_version" value="${Number(a.version)||0}"><h4>投递资料</h4><label>所用简历版本<input name="resume_version" value="${esc(a.resume_version||'')}" maxlength="60" placeholder="例如：后端版 · 9月29日"></label><label>投递备注（可选）<textarea name="note" maxlength="600" placeholder="记录渠道、联系情况或需要跟进的事项">${esc(a.note||'')}</textarea></label><button class="btn">保存投递资料</button></form></div></details>${followUpEditor(a,{esc})}<div id="application-history-${esc(a.id)}"></div></article>`;}).join('')}</div>`;
  }
  function ended(row){return ['OFFER','REJECTED','WITHDRAWN'].includes(row.current_state);}
  function filtered(rows,{query='',company='',stage='',followUpOnly=false,sort='updated',now=Date.now()}={}){
    const term=query.trim().toLowerCase();
    return rows.filter(a=>(!term||[a.job?.company,a.job?.title,a.next_action].join(' ').toLowerCase().includes(term))&&(!company||a.job?.company===company)&&(!stage||a.current_state===stage)&&(!followUpOnly||dueToday(a,now))).sort((a,b)=>{
      const stamp=value=>Number.isFinite(Date.parse(value))?Date.parse(value):null;
      if(sort==='followup'){const x=hasFollowUp(a)?stamp(a.follow_up_at):null,y=hasFollowUp(b)?stamp(b.follow_up_at):null;if(x!==y)return x==null?1:y==null?-1:x-y;}
      if(sort==='applied'){const x=stamp(a.applied_at),y=stamp(b.applied_at);if(x!==y)return x==null?1:y==null?-1:y-x;}
      return (stamp(b.updated_at)||0)-(stamp(a.updated_at)||0)||String(a.id).localeCompare(String(b.id));
    });
  }
  function pageSlice(rows,page=1,size=25){const pages=Math.max(1,Math.ceil(rows.length/size)),current=Math.max(1,Math.min(pages,page));return {rows:rows.slice((current-1)*size,current*size),page:current,pages};}
  async function page(set,heading,{api,esc,D,formAction,navigate,scheduleInterview,table,initialApplication='',active=()=>true}){
    const [cap,records]=await Promise.all([api('/api/profile/resume/capabilities'),api('/api/applications')]);if(!active())return;
    let rows=records,localDrafts=null;const N=root.CampusNavigation,saved=N?.readWorkflow(cap.user_id,'applications')||{};
    let initialNotice='';
    if(initialApplication&&!rows.some(row=>row.id===initialApplication)){try{rows.push(await api('/api/applications/'+encodeURIComponent(initialApplication)));}catch(error){initialNotice=error.message;}}
    if(!active())return;
    let query=initialApplication?'':saved.query||'',company=initialApplication?'':saved.company||'',stage=initialApplication?'':saved.stage||'',openEnded=!!saved.openEnded,ongoingPage=saved.ongoingPage||1,endedPage=saved.endedPage||1,sort=['updated','applied','followup'].includes(saved.applicationSort)?saved.applicationSort:'updated',followUpOnly=!initialApplication&&saved.followUpOnly===true,busy=false,campaigns=null;
    const editors=new Set(),drafts=root.CampusNavigation?.forms(document,{selector:'#application-records form',retainMissing:true});
    const localTracker=N?.forms(document,{selector:'#application-records form',retainMissing:true,ignoreNames:['resume_version']});
    const companies=[...new Set(rows.map(a=>a.job?.company).filter(Boolean))].sort((a,b)=>a.localeCompare(b,'zh'));
    if(company&&!companies.includes(company))company='';if(stage&&!Object.hasOwn(D.enums.application,stage))stage='';
    const select=(values,blank)=>`<option value="">${blank}</option>${values.map(([v,label])=>`<option value="${esc(v)}">${esc(label)}</option>`).join('')}`;
    if(!set(heading+`<form id="application-filter" class="workflow-filter application-filters"><label>搜索公司、岗位或跟进事项<input id="application-search" type="search" placeholder="例如：小红书、后端开发"></label><label>公司<select id="application-company">${select(companies.map(c=>[c,c]),'所有公司')}</select></label><label>投递阶段<select id="application-stage">${select(Object.entries(D.enums.application),'所有阶段')}</select></label><label>排序<select id="application-sort"><option value="updated">最近更新</option><option value="applied">投递时间（最新在前）</option><option value="followup">跟进时间（最早在前）</option></select></label><button type="button" id="application-reset" class="btn btn-subtle">清除筛选</button></form><div class="application-tools"><label class="quiet-check"><input id="application-follow-up-only" type="checkbox">只看今天需跟进</label><button id="application-refresh" class="btn btn-subtle btn-small">刷新记录</button></div><p id="application-summary" class="meta" role="status"></p><section id="local-drafts" class="local-drafts" aria-label="投递编辑草稿"></section><section id="application-records"></section>`+(root.CampusCampaigns?'<section id="application-campaigns"></section>':'')))return;
    const q=s=>document.querySelector(s),notify=message=>root.CampusUI.notify(message);
    function replaceRows(latest){
      rows=latest;
      const values=[...new Set(rows.map(a=>a.job?.company).filter(Boolean))].sort((a,b)=>a.localeCompare(b,'zh'));
      if(company&&!values.includes(company))company='';
      q('#application-company').innerHTML=select(values.map(c=>[c,c]),'所有公司');q('#application-company').value=company;
    }
    const go=(...args)=>Promise.resolve(navigate(...args)).catch(error=>notify(error.message));
    const savePosition=()=>N?.storeWorkflow(cap.user_id,'applications',{query:q('#application-search').value,company,stage,openEnded,ongoingPage,endedPage,applicationSort:sort,followUpOnly,scroll:root.scrollY||0});
    const checkpoint=()=>{savePosition();localDrafts?.flush();};
    root.CampusNavigation?.register({active,checkpoint,dirty:()=>busy||drafts?.dirty()||campaigns?.dirty(),dispose:()=>localDrafts?.dispose()});
    const pager=(v,group)=>v.pages>1?`<div class="workflow-pagination"><span>第 ${v.page} / ${v.pages} 页</span><button class="btn btn-small" data-application-page="${group}" data-number="${v.page-1}" ${v.page===1?'disabled':''}>上一页</button><button class="btn btn-small" data-application-page="${group}" data-number="${v.page+1}" ${v.page===v.pages?'disabled':''}>下一页</button></div>`:'';
    const capture=()=>{drafts?.capture();localTracker?.capture();for(const el of q('#application-records').querySelectorAll('[data-application-editor]'))el.open?editors.add(el.dataset.applicationEditor):editors.delete(el.dataset.applicationEditor);};
    function draw(){
      if(!active())return;capture();
      const visible=filtered(rows,{query,company,stage,followUpOnly,sort}),ongoing=pageSlice(visible.filter(a=>!ended(a)),ongoingPage),finished=pageSlice(visible.filter(ended),endedPage);
      ongoingPage=ongoing.page;endedPage=finished.page;
      q('#application-summary').textContent=`符合筛选 ${visible.length} 条 · 进行中 ${visible.filter(a=>!ended(a)).length} 条 · 已结束 ${visible.filter(ended).length} 条`+` · 今天需跟进 ${visible.filter(a=>dueToday(a)).length} 条`+(rows.length>=500?'。当前载入最近 500 条记录。':'');
      q('#application-records').innerHTML=!rows.length?render([],{esc,D}):!visible.length?'<p class="empty">没有符合条件的投递记录，请调整或清除筛选。</p>':`<div class="section-heading"><h3>进行中的投递</h3></div>${ongoing.rows.length?render(ongoing.rows,{esc,D}):'<p class="empty">当前筛选下没有进行中的投递。</p>'}${pager(ongoing,'ongoing')}${finished.rows.length?`<details id="application-ended" class="workflow-ended" ${openEnded?'open':''}><summary>已结束的投递 · ${visible.filter(ended).length} 条</summary>${render(finished.rows,{esc,D})}${pager(finished,'ended')}</details>`:''}`;
      for(const el of q('#application-records').querySelectorAll('[data-application-editor]'))el.open=editors.has(el.dataset.applicationEditor);
      drafts?.restore();localTracker?.restore();localDrafts?.draw();
      const finishedBox=q('#application-ended');if(finishedBox)finishedBox.ontoggle=()=>{openEnded=finishedBox.open;};
      for(const button of q('#application-records').querySelectorAll('[data-application-page]'))button.onclick=()=>{capture();if(button.dataset.applicationPage==='ended')endedPage=Number(button.dataset.number);else ongoingPage=Number(button.dataset.number);draw();};
      for(const a of [...ongoing.rows,...finished.rows]){
        const article=q(`[data-application-id="${a.id}"]`);if(!article)continue;
        article.querySelector('[data-application-job]').onclick=()=>go('matching',{jobID:a.job_id});
        article.querySelector('[data-application-preparation]').onclick=()=>go('matching',{jobID:a.job_id,view:'preparation'});
        article.querySelector('[data-application-interview]').onclick=()=>Promise.resolve(scheduleInterview(a.id)).catch(error=>notify(error.message));
        article.querySelector('[data-application-history]').onclick=async()=>{try{const history=await api('/api/applications/'+encodeURIComponent(a.id)+'/history');if(active()){const box=q('#application-history-'+a.id);if(box)box.innerHTML='<h4>投递进展记录</h4>'+table(history,['from_state','to_state','occurred_at','note'],'暂未记录进展变化。');}}catch(error){if(active())notify(error.message);}};
        if(a.next_states?.length)formAction('#application-stage-'+a.id,async data=>save('#application-stage-'+a.id,()=>api('/api/applications/transition','POST',{application_id:a.id,state:data.get('state'),version:a.version,note:String(data.get('note')||'').trim()}),'投递进展已更新。'));
        formAction('#application-details-'+a.id,async data=>save('#application-details-'+a.id,()=>api('/api/applications/'+encodeURIComponent(a.id),'PUT',{version:a.version,resume_version:String(data.get('resume_version')||'').trim(),note:String(data.get('note')||'').trim()}),'投递资料已保存。'));
        if(!ended(a)){
          const selector='#application-follow-up-'+a.id;
          formAction(selector,async data=>{const at=followUpTime(data.get('follow_up_at'));if(!at){root.CampusUI.formStatus?.(q(selector),'请填写有效的跟进时间（北京时间）。','error');return false;}await save(selector,()=>api('/api/applications/'+encodeURIComponent(a.id)+'/follow-up','PUT',{version:a.version,follow_up_at:at,next_action:String(data.get('next_action')||'').trim()}),'跟进提醒已保存。');});
          for(const button of article.querySelectorAll('[data-follow-up-days]'))button.onclick=()=>{const input=q(selector).elements.follow_up_at;input.value=followUpShortcut(Number(button.dataset.followUpDays));input.dispatchEvent(new Event('input',{bubbles:true}));};
          const complete=article.querySelector('[data-application-follow-up-complete]'),cancel=article.querySelector('[data-application-follow-up-cancel]');
          if(complete)complete.onclick=()=>save(selector,()=>api('/api/applications/'+encodeURIComponent(a.id)+'/follow-up/complete','POST',{version:a.version}),'已完成跟进。',true).catch(error=>notify(error.message));
          if(cancel)cancel.onclick=()=>save(selector,()=>api('/api/applications/'+encodeURIComponent(a.id)+'/follow-up','PUT',{version:a.version,follow_up_at:null,next_action:''}),'跟进提醒已取消。',true).catch(error=>notify(error.message));
        }
      }
      for(const button of q('#application-records').querySelectorAll('form button'))button.disabled=busy;
    }
    async function save(selector,request,message,discard=false){
      if(busy)return false;busy=true;const point=drafts?.savepoint(selector),localPoint=localTracker?.savepoint(selector);root.CampusUI.formStatus?.(q(selector),'正在保存…','pending');
      try{
        const saved=await request();if(!active())return;
        if(discard){const form=q(selector);if(form){form.elements.follow_up_at.value='';form.elements.next_action.value='';}drafts?.forget(selector);localTracker?.forget(selector);}else{drafts?.saved(selector,point);localTracker?.saved(selector,localPoint);}capture();
        rows=rows.map(a=>a.id===saved.id?{...a,...saved,follow_up_at:saved.follow_up_at??null,next_action:saved.next_action||'',note:saved.note||'',next_states:Array.isArray(saved.next_states)?saved.next_states:saved.current_state===a.current_state?a.next_states:[]}:a);
        try{const latest=await api('/api/applications');if(!active())return;replaceRows(latest);}catch{message+=' 列表刷新失败，可点「刷新记录」读取最新进展。';}
        if(active()){busy=false;draw();localDrafts?.flush();notify(message);root.CampusUI.formStatus?.(q(selector),message,'success');}
      return true;}catch(error){if(active())root.CampusUI.formStatus?.(q(selector),error.message,'error');throw error;}finally{busy=false;if(active())for(const button of q('#application-records').querySelectorAll('form button'))button.disabled=false;}
    }
    function applyFilters(){query=q('#application-search').value;company=q('#application-company').value;stage=q('#application-stage').value;sort=q('#application-sort').value;followUpOnly=q('#application-follow-up-only').checked;ongoingPage=endedPage=1;openEnded=!!(query.trim()||company||stage)&&!followUpOnly;draw();}
    q('#application-filter').onsubmit=e=>{e.preventDefault();applyFilters();};q('#application-search').oninput=applyFilters;q('#application-company').onchange=applyFilters;q('#application-stage').onchange=applyFilters;q('#application-sort').onchange=applyFilters;q('#application-follow-up-only').onchange=applyFilters;
    q('#application-reset').onclick=()=>{q('#application-search').value=q('#application-company').value=q('#application-stage').value='';q('#application-follow-up-only').checked=false;q('#application-sort').value='updated';applyFilters();};
    if(initialApplication){const a=rows.find(a=>a.id===initialApplication);if(a){const group=filtered(rows,{sort}).filter(row=>ended(row)===ended(a));if(ended(a)){openEnded=true;endedPage=Math.floor(group.indexOf(a)/25)+1;}else ongoingPage=Math.floor(group.indexOf(a)/25)+1;}}
    q('#application-search').value=query;q('#application-company').value=company;q('#application-stage').value=stage;q('#application-sort').value=sort;q('#application-follow-up-only').checked=followUpOnly;
    q('#application-refresh').onclick=async()=>{if(busy)return;busy=true;const button=q('#application-refresh');button.disabled=true;try{const latest=await api('/api/applications');if(!active())return;replaceRows(latest);draw();notify('投递记录已刷新。');}catch(error){if(active())notify(error.message);}finally{busy=false;if(active()){button.disabled=false;draw();}}};
    draw();
    localDrafts=root.CampusDrafts?.mount({user:cap.user_id,scope:'applications',tracker:localTracker,active,allow:id=>id.startsWith('application-details-')||id.startsWith('application-follow-up-'),help:'仅在本机保留备注与跟进事项；简历版本不保留。',afterRestore:()=>{drafts?.capture();draw();for(const el of q('#application-records').querySelectorAll('[data-application-editor]')){if(localTracker?.entries().some(v=>v.id==='application-details-'+el.dataset.applicationEditor||v.id==='application-follow-up-'+el.dataset.applicationEditor.replace(/:follow$/,''))){el.open=true;editors.add(el.dataset.applicationEditor);}}}});
    if(!initialApplication)root.requestAnimationFrame?.(()=>{if(active())root.scrollTo?.({top:saved.scroll||0,behavior:'instant'});});
    if(initialNotice)notify(initialNotice);
    if(initialApplication){const article=q(`[data-application-id="${initialApplication}"]`);if(article){article.classList.add('application-current');article.scrollIntoView({block:'center',behavior:'instant'});article.querySelector('[data-application-job]').focus({preventScroll:true});}}
    if(root.CampusCampaigns)root.CampusCampaigns.mount(q('#application-campaigns'),{api,esc,active,notify}).then(controller=>{if(active())campaigns=controller;}).catch(error=>{if(active())notify(error.message);});
  }
  const api={render,page,officialLink,ended,filtered,pageSlice,hasFollowUp,dueToday,followUpInput,followUpTime,followUpShortcut};if(typeof module==='object'&&module.exports)module.exports=api;root.CampusApplications=api;return api;
})(typeof window==='undefined'?globalThis:window);
