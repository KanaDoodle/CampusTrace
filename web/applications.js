'use strict';
const CampusApplications=(function(root){
  function officialLink(value){try{const u=new URL(value);return ['http:','https:'].includes(u.protocol)&&!u.username&&!u.password?u.href:'';}catch{return '';}}
  function render(rows,{esc,D}){
    if(!rows.length)return '<p class="empty">还没有投递记录。可在岗位详情中将心仪岗位加入投递计划。</p>';
    return `<div class="application-list">${rows.map(a=>{const j=a.job,url=officialLink(a.official_url),states=a.next_states||[];return `<article class="application-record" data-application-id="${esc(a.id)}"><div class="application-head"><div><p class="application-company">${esc(j?.company||'公司信息暂不可用')}</p><h3><button class="text-btn application-title" data-application-job="${esc(a.job_id)}">${esc(j?.title||'查看岗位记录')}</button></h3><p class="meta">${esc((j?.locations||[]).join(' / '))}${j?.job_type?' · '+esc(D.label('job_type',j.job_type)):''}</p></div><span class="pill">${esc(D.label('application',a.current_state))}</span></div><dl class="application-info"><div><dt>投递时间</dt><dd>${a.applied_at?esc(D.date(a.applied_at)):'尚未记录投递'}</dd></div><div><dt>所用简历版本</dt><dd>${esc(a.resume_version||'尚未记录')}</dd></div><div><dt>最近更新</dt><dd>${esc(D.date(a.updated_at))}</dd></div></dl>${a.note?`<p class="application-note">${esc(a.note)}</p>`:''}<div class="actions">${url?`<a class="btn btn-small" href="${esc(url)}" target="_blank" rel="noopener noreferrer">查看招聘官网</a>`:''}<button class="btn btn-small" data-application-preparation="${esc(a.job_id)}">岗位准备清单</button><button class="btn btn-small" data-application-interview="${esc(a.id)}">记录面试安排</button><button class="btn btn-small" data-application-history="${esc(a.id)}">查看进展记录</button></div><details data-application-editor="${esc(a.id)}"><summary>更新进展与投递资料</summary><div class="application-edit">${states.length?`<form id="application-stage-${esc(a.id)}"><h4>更新进展</h4><label>下一阶段<select name="state">${states.map(v=>`<option value="${esc(v)}">${esc(D.label('application',v))}</option>`).join('')}</select></label><label>本次进展说明（可选）<textarea name="note" maxlength="600" placeholder="例如：完成笔试，等待面试通知"></textarea></label><button class="btn">保存进展</button></form>`:'<p class="meta">这条投递已结束，仍可补充资料和查看历史记录。</p>'}<form id="application-details-${esc(a.id)}"><h4>投递资料</h4><label>所用简历版本<input name="resume_version" value="${esc(a.resume_version||'')}" maxlength="60" placeholder="例如：后端版 · 9月29日"></label><label>投递备注（可选）<textarea name="note" maxlength="600" placeholder="记录渠道、联系情况或需要跟进的事项">${esc(a.note||'')}</textarea></label><button class="btn">保存投递资料</button></form></div></details><div id="application-history-${esc(a.id)}"></div></article>`;}).join('')}</div>`;
  }
  function ended(row){return ['OFFER','REJECTED','WITHDRAWN'].includes(row.current_state);}
  function filtered(rows,{query='',company='',stage=''}={}){
    const term=query.trim().toLowerCase();
    return rows.filter(a=>(!term||[a.job?.company,a.job?.title].join(' ').toLowerCase().includes(term))&&(!company||a.job?.company===company)&&(!stage||a.current_state===stage));
  }
  function pageSlice(rows,page=1,size=25){const pages=Math.max(1,Math.ceil(rows.length/size)),current=Math.max(1,Math.min(pages,page));return {rows:rows.slice((current-1)*size,current*size),page:current,pages};}
  async function page(set,heading,{api,esc,D,formAction,navigate,scheduleInterview,table,initialApplication='',active=()=>true}){
    let rows=await api('/api/applications');if(!active())return;
    let query='',company='',stage='',openEnded=false,ongoingPage=1,endedPage=1,busy=false,campaigns=null;
    const editors=new Set(),drafts=root.CampusNavigation?.forms(document,{selector:'#application-records form',retainMissing:true});
    const companies=[...new Set(rows.map(a=>a.job?.company).filter(Boolean))].sort((a,b)=>a.localeCompare(b,'zh'));
    const select=(values,blank)=>`<option value="">${blank}</option>${values.map(([v,label])=>`<option value="${esc(v)}">${esc(label)}</option>`).join('')}`;
    if(!set(heading+`<p class="form-note">记录实际投递与后续进展。岗位关闭不会自动结束已提交的申请。</p><form id="application-filter" class="workflow-filter"><label>搜索公司或岗位<input id="application-search" type="search" placeholder="例如：小红书、后端开发"></label><label>公司<select id="application-company">${select(companies.map(c=>[c,c]),'所有公司')}</select></label><label>投递阶段<select id="application-stage">${select(Object.entries(D.enums.application),'所有阶段')}</select></label><button type="button" id="application-reset" class="btn btn-subtle">清除筛选</button></form><p id="application-summary" class="meta" role="status"></p><section id="application-records"></section>`+(root.CampusCampaigns?'<section id="application-campaigns"></section>':'')))return;
    const q=s=>document.querySelector(s),notify=message=>root.CampusUI.notify(message);
    const go=(...args)=>Promise.resolve(navigate(...args)).catch(error=>notify(error.message));
    root.CampusNavigation?.register({active,dirty:()=>busy||drafts?.dirty()||campaigns?.dirty()});
    const pager=(v,group)=>v.pages>1?`<div class="workflow-pagination"><span>第 ${v.page} / ${v.pages} 页</span><button class="btn btn-small" data-application-page="${group}" data-number="${v.page-1}" ${v.page===1?'disabled':''}>上一页</button><button class="btn btn-small" data-application-page="${group}" data-number="${v.page+1}" ${v.page===v.pages?'disabled':''}>下一页</button></div>`:'';
    const capture=()=>{drafts?.capture();for(const el of q('#application-records').querySelectorAll('[data-application-editor]'))el.open?editors.add(el.dataset.applicationEditor):editors.delete(el.dataset.applicationEditor);};
    function draw(){
      if(!active())return;capture();
      const visible=filtered(rows,{query,company,stage}),ongoing=pageSlice(visible.filter(a=>!ended(a)),ongoingPage),finished=pageSlice(visible.filter(ended),endedPage);
      ongoingPage=ongoing.page;endedPage=finished.page;
      q('#application-summary').textContent=`符合筛选 ${visible.length} 条 · 进行中 ${visible.filter(a=>!ended(a)).length} 条 · 已结束 ${visible.filter(ended).length} 条`+(rows.length>=500?'。当前载入最近 500 条记录。':'');
      q('#application-records').innerHTML=!rows.length?render([],{esc,D}):!visible.length?'<p class="empty">没有符合条件的投递记录，请调整或清除筛选。</p>':`<div class="section-heading"><h3>进行中的投递</h3></div>${ongoing.rows.length?render(ongoing.rows,{esc,D}):'<p class="empty">当前筛选下没有进行中的投递。</p>'}${pager(ongoing,'ongoing')}${finished.rows.length?`<details id="application-ended" class="workflow-ended" ${openEnded?'open':''}><summary>已结束的投递 · ${visible.filter(ended).length} 条</summary>${render(finished.rows,{esc,D})}${pager(finished,'ended')}</details>`:''}`;
      for(const el of q('#application-records').querySelectorAll('[data-application-editor]'))el.open=editors.has(el.dataset.applicationEditor);
      drafts?.restore();
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
      }
      for(const button of q('#application-records').querySelectorAll('form button'))button.disabled=busy;
    }
    async function save(selector,request,message){
      if(busy)return;busy=true;const point=drafts?.savepoint(selector);
      try{
        const saved=await request();if(!active())return;
        drafts?.saved(selector,point);capture();
        rows=rows.map(a=>a.id===saved.id?{...a,...saved,next_states:[]}:a);
        try{const latest=await api('/api/applications');if(!active())return;rows=latest;}catch{message+=' 列表刷新失败，请稍后重新进入以读取最新进展。';}
        if(active()){busy=false;draw();notify(message);}
      }finally{busy=false;if(active())for(const button of q('#application-records').querySelectorAll('form button'))button.disabled=false;}
    }
    function applyFilters(){query=q('#application-search').value;company=q('#application-company').value;stage=q('#application-stage').value;ongoingPage=endedPage=1;openEnded=!!(query.trim()||company||stage);draw();}
    q('#application-filter').onsubmit=e=>{e.preventDefault();applyFilters();};q('#application-search').oninput=applyFilters;q('#application-company').onchange=applyFilters;q('#application-stage').onchange=applyFilters;
    q('#application-reset').onclick=()=>{q('#application-search').value=q('#application-company').value=q('#application-stage').value='';applyFilters();};
    if(initialApplication){const a=rows.find(a=>a.id===initialApplication);if(a){const group=rows.filter(row=>ended(row)===ended(a));if(ended(a)){openEnded=true;endedPage=Math.floor(group.indexOf(a)/25)+1;}else ongoingPage=Math.floor(group.indexOf(a)/25)+1;}}
    draw();
    if(initialApplication){const article=q(`[data-application-id="${initialApplication}"]`);if(article){article.classList.add('application-current');article.scrollIntoView({block:'center',behavior:'instant'});article.querySelector('[data-application-job]').focus({preventScroll:true});}}
    if(root.CampusCampaigns)root.CampusCampaigns.mount(q('#application-campaigns'),{api,esc,active,notify}).then(controller=>{if(active())campaigns=controller;}).catch(error=>{if(active())notify(error.message);});
  }
  const api={render,page,officialLink,ended,filtered,pageSlice};if(typeof module==='object'&&module.exports)module.exports=api;root.CampusApplications=api;return api;
})(typeof window==='undefined'?globalThis:window);
