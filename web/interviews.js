'use strict';
const CampusInterviews=(function(root){
  function completed(row){return !!row.finished_at&&!String(row.finished_at).startsWith('0001-');}
  function filtered(rows,{query='',company='',view='all'}={}){
    const term=query.trim().toLowerCase();
    return rows.filter(row=>(!term||[row.job?.company,row.job?.title].join(' ').toLowerCase().includes(term))&&(!company||row.job?.company===company)&&({all:true,pending:!completed(row),review:completed(row)&&!row.review,completed:completed(row)})[view]).sort((a,b)=>{
      if(completed(a)!==completed(b))return completed(a)?1:-1;
      const delta=completed(a)?Date.parse(b.finished_at)-Date.parse(a.finished_at):Date.parse(a.scheduled_at)-Date.parse(b.scheduled_at);
      return (Number.isFinite(delta)?delta:0)||a.id.localeCompare(b.id);
    });
  }
  function render(rows,{esc,D,now=Date.now()}){
    if(!rows.length)return '<p class="empty">当前没有符合条件的面试。可调整筛选，或从投递进展中记录面试安排。</p>';
    const lines=values=>`<ul>${(values||[]).map(value=>`<li>${esc(value)}</li>`).join('')}</ul>`;
    return `<div class="application-list interview-list">${rows.map(row=>{
      const done=completed(row),past=Date.parse(row.scheduled_at)<now,status=done?D.label('interview',row.result||'PENDING'):past?'待记录完成情况':'待面试',review=row.review;
      return `<article class="application-record" data-workbench-row="interview-${esc(row.id)}" data-interview-id="${esc(row.id)}"><div class="application-head"><div><p class="application-company">${esc(row.job?.company||'公司信息暂不可用')}</p><h3 class="interview-title">${esc(row.job?.title||'岗位信息暂不可用')}</h3><p class="meta">第 ${Number(row.round)} 轮面试${row.job?.locations?.length?' · '+esc(row.job.locations.join(' / ')):''}</p></div><span class="badge badge-${done?'green':past?'amber':'blue'}">${esc(status)}</span></div><dl class="application-info"><div><dt>面试安排</dt><dd>${esc(D.date(row.scheduled_at))}</dd></div><div><dt>完成情况</dt><dd>${done?'已记录完成':'尚未记录完成'}</dd></div><div><dt>本轮复盘</dt><dd>${review?'已填写':done?'待复盘':'尚未填写'}</dd></div></dl>${row.notes?`<p class="application-note">${esc(row.notes)}</p>`:''}${done?`<p class="meta">首次记录完成：${esc(D.date(row.finished_at))}</p>`:''}<div class="actions"><button class="btn btn-small" data-interview-application="${esc(row.application_id)}">查看投递进展</button>${row.job?`<button class="btn btn-small" data-interview-preparation="${esc(row.job.id)}">岗位准备清单</button>`:''}${!review?`<button class="btn btn-small" data-interview-review="${esc(row.id)}">填写本轮复盘</button>`:''}</div><details data-interview-editor="${esc(row.id)}"><summary>${done?'更新本轮反馈':'记录本轮完成情况'}</summary><form id="interview-result-${esc(row.id)}" class="interview-result"><p class="form-note">${done?'补充收到的反馈，保留首次记录完成的时间。':'保存后将本轮标记为已完成；尚未收到反馈时选择“已完成，等待反馈”。'}</p><label>本轮结果<select id="interview-result-value-${esc(row.id)}" name="result">${[['PENDING','已完成，等待反馈'],['PASS','本轮通过'],['FAIL','本轮未通过']].map(([value,label])=>`<option value="${value}" ${value===row.result?'selected':''}>${label}</option>`).join('')}</select></label><label>本轮备注（可选）<textarea id="interview-notes-${esc(row.id)}" name="notes" maxlength="2000" placeholder="记录本轮面试和实际收到的反馈">${esc(row.notes||'')}</textarea></label><button class="btn btn-primary">${done?'保存反馈':'保存完成情况'}</button></form></details>${review?`<details class="interview-review" data-interview-editor="${esc(row.id)}:review"><summary>查看本轮复盘</summary><h4>实际被问到的问题</h4>${lines(review.actual_questions)}<h4>自我复盘</h4><p>${esc(review.self_evaluation)}</p>${review.missed_points?.length?'<h4>未答好的要点</h4>'+lines(review.missed_points):''}${review.follow_up_notes?'<h4>后续复习计划</h4><p>'+esc(review.follow_up_notes)+'</p>':''}</details>`:''}</article>`;
    }).join('')}</div>`;
  }
  async function page(set,heading,{api,esc,D,formAction,navigate,reviewInterview,initialInterview='',initialView='',resume=false,active=()=>true}){
    const firstRead=api('/api/interviews').then(value=>({value}),error=>({error}));
    const cap=await api('/api/profile/resume/capabilities');if(!active())return;
    const N=root.CampusNavigation,saved=N?.readWorkflow(cap.user_id,'interviews')||{},U=root.CampusUI;
    let rows=[],loaded=false,loading=false,busy=false,error='',stopped=false,composing=false,pendingDraw=false,localDrafts=null;
    let query=initialInterview?'':saved.query||'',company=initialInterview?'':saved.company||'',view=['all','pending','review','completed'].includes(initialView)?initialView:saved.view||'all',pageNumber=initialView||initialInterview?1:saved.page||1,lastInterview=saved.lastInterview||'',targetPending=!!initialInterview;
    if(!['all','pending','review','completed'].includes(view))view='all';
    if(initialInterview)view='all';
    const alive=()=>!stopped&&active(),editors=new Set(saved.openIDs||[]),drafts=N?.forms(document,{selector:'#interview-records form',retainMissing:true});
    if(!set(heading+`<form id="interview-filter" class="workflow-filter"><label>搜索公司或岗位<input id="interview-search" type="search" maxlength="200" placeholder="例如：小红书、后端开发"></label><label>公司<select id="interview-company"><option value="">所有公司</option></select></label><label>面试进度<select id="interview-view"><option value="all">全部面试</option><option value="pending">待面试／待记录完成</option><option value="review">待复盘</option><option value="completed">已完成</option></select></label><button type="button" id="interview-reset" class="btn btn-subtle">清除筛选</button></form><div class="application-tools"><p id="interview-summary" class="meta" role="status"></p><button id="interview-topics" class="btn btn-subtle btn-small">待加强知识点</button><button id="interview-refresh" class="btn btn-subtle btn-small">刷新记录</button></div><div id="interview-feedback" role="status" aria-live="polite"></div><section id="local-drafts" class="local-drafts" aria-label="面试反馈草稿"></section><section id="interview-records"></section>`))return;
    const q=s=>document.querySelector(s),host=q('#interview-records'),notify=message=>U.notify(message);
    const go=(...args)=>Promise.resolve(navigate(...args)).catch(failure=>{if(alive())notify(failure.message);});
    q('#interview-topics').onclick=()=>go('weak_topics');
    const openEditors=()=>[...host.querySelectorAll('[data-interview-editor]')].filter(el=>el.open).map(el=>el.dataset.interviewEditor);
    const savePosition=()=>N?.storeWorkflow(cap.user_id,'interviews',{query:q('#interview-search').value,company,view,page:pageNumber,lastInterview,openIDs:openEditors(),scroll:root.scrollY||0});
    const checkpoint=()=>{savePosition();localDrafts?.flush();};
    N?.register({active:alive,checkpoint,dispose:()=>{stopped=true;localDrafts?.dispose();},dirty:()=>busy||drafts?.dirty()});
    const paint=(el,html)=>U.reconcile?U.reconcile(el,html):el.innerHTML=html;
    function updateCompanies(){
      const companies=[...new Set(rows.map(row=>row.job?.company).filter(Boolean))].sort((a,b)=>a.localeCompare(b,'zh'));
      if(company&&!companies.includes(company))company='';
      q('#interview-company').innerHTML='<option value="">所有公司</option>'+companies.map(c=>`<option>${esc(c)}</option>`).join('');q('#interview-company').value=company;
    }
    function draw(){
      if(!alive())return;if(composing){pendingDraw=true;return;}pendingDraw=false;
      const position=U.capture?.();drafts?.capture();
      for(const el of host.querySelectorAll('[data-interview-editor]'))el.open?editors.add(el.dataset.interviewEditor):editors.delete(el.dataset.interviewEditor);
      const visible=filtered(rows,{query,company,view}),pages=Math.max(1,Math.ceil(visible.length/25));if(loaded)pageNumber=Math.min(pages,pageNumber);
      const slice=visible.slice((pageNumber-1)*25,pageNumber*25);
      q('#interview-summary').textContent=loaded?`待面试／待记录完成 ${rows.filter(row=>!completed(row)).length} 轮 · 待复盘 ${rows.filter(row=>completed(row)&&!row.review).length} 轮 · 当前筛选 ${visible.length} 轮`+(rows.length>=500?'。当前载入最近 500 轮面试。':''):'';
      q('#interview-refresh').disabled=loading||busy;q('#interview-refresh').textContent=loading?'正在刷新…':'刷新记录';
      const feedback=error?`<p class="pending-note">${esc(error)}${loaded?' 当前保留上次读取的面试记录。':''}</p><button id="interview-retry" class="btn btn-small" ${loading||busy?'disabled':''}>重新读取记录</button>`:loading?'<p class="meta">正在读取面试记录…</p>':'';
      paint(q('#interview-feedback'),feedback);const retry=q('#interview-retry');if(retry)retry.onclick=()=>load();
      paint(host,loaded?render(slice,{esc,D})+(pages>1?`<div class="workflow-pagination"><span>第 ${pageNumber} / ${pages} 页</span><button class="btn btn-small" data-interview-page="${pageNumber-1}" ${pageNumber===1?'disabled':''}>上一页</button><button class="btn btn-small" data-interview-page="${pageNumber+1}" ${pageNumber===pages?'disabled':''}>下一页</button></div>`:''):'');
      for(const el of host.querySelectorAll('[data-interview-editor]'))el.open=editors.has(el.dataset.interviewEditor);
      drafts?.restore();localDrafts?.draw();
      for(const button of host.querySelectorAll('[data-interview-page]'))button.onclick=()=>{pageNumber=Number(button.dataset.interviewPage);draw();host.scrollIntoView?.({block:'start',behavior:'instant'});};
      for(const row of slice){
        const article=[...host.querySelectorAll('[data-interview-id]')].find(el=>el.dataset.interviewId===row.id);if(!article)continue;
        article.querySelector('[data-interview-application]').onclick=()=>go('applications',{applicationID:row.application_id});
        const preparation=article.querySelector('[data-interview-preparation]');if(preparation)preparation.onclick=()=>{lastInterview=row.id;savePosition();return go('matching',{jobID:row.job.id,view:'preparation',returnTo:{page:'interviews'}});};
        const review=article.querySelector('[data-interview-review]');if(review)review.onclick=()=>Promise.resolve(reviewInterview(row.id)).catch(failure=>{if(alive())notify(failure.message);});
      }
      for(const row of slice)formAction('#interview-result-'+row.id,async data=>{
        if(busy||loading)return false;busy=true;const selector='#interview-result-'+row.id,point=drafts?.savepoint(selector);
        q('#interview-refresh').disabled=true;for(const button of host.querySelectorAll('form button'))button.disabled=true;
        try{
          const saved=await api('/api/interviews/'+encodeURIComponent(row.id)+'/finish','POST',{result:data.get('result'),notes:String(data.get('notes')||'').trim()});if(!alive())return;
          drafts?.saved(selector,point);rows=rows.map(old=>old.id===saved.id?{...old,...saved}:old);localDrafts?.flush();notify('本轮面试完成情况已保存。');
        }finally{busy=false;if(alive())draw();}
      },{success:'本轮面试完成情况已保存。'});
      for(const button of host.querySelectorAll('form button'))button.disabled=busy||loading;
      U.restore?.(position);
    }
    async function load(initial=null){
      if(!alive()||loading||busy)return;loading=true;error='';draw();
      try{
        const result=initial?await initial:{value:await api('/api/interviews')};if(!alive())return;
        if(result.error)throw result.error;if(!Array.isArray(result.value))throw Error('面试记录暂时无法读取，请重试。');
        rows=result.value;loaded=true;
        if(targetPending&&!rows.some(row=>row.id===initialInterview)){
          try{const row=await api('/api/interviews/'+encodeURIComponent(initialInterview));if(!alive())return;rows.push(row);}catch(failure){if(alive())notify(failure.message);}
        }
        if(!alive())return;updateCompanies();
        if(targetPending){const index=filtered(rows,{view}).findIndex(row=>row.id===initialInterview);if(index>=0)pageNumber=Math.floor(index/25)+1;targetPending=false;}
      }catch(failure){if(alive())error=failure.message;}
      finally{loading=false;if(alive())draw();}
    }
    function applyFilters(){query=q('#interview-search').value;company=q('#interview-company').value;view=q('#interview-view').value;pageNumber=1;draw();}
    const search=q('#interview-search');
    q('#interview-filter').onsubmit=e=>{e.preventDefault();if(!composing)applyFilters();};
    search.oncompositionstart=()=>composing=true;search.oncompositionend=()=>{composing=false;applyFilters();};search.oninput=e=>{if(!composing&&!e.isComposing)applyFilters();};
    host.oncompositionstart=()=>composing=true;host.oncompositionend=()=>{composing=false;if(pendingDraw)draw();};
    q('#interview-company').onchange=applyFilters;q('#interview-view').onchange=applyFilters;
    q('#interview-reset').onclick=()=>{search.value=q('#interview-company').value='';q('#interview-view').value='all';applyFilters();};
    q('#interview-refresh').onclick=()=>load();q('#interview-view').value=view;search.value=query;
    await load(firstRead);if(!alive())return;
    localDrafts=root.CampusDrafts?.mount({user:cap.user_id,scope:'interview-feedback',tracker:drafts,active:alive,allow:id=>id.startsWith('interview-result-'),help:'仅在本机保留面试反馈与备注。',afterRestore:()=>{draw();for(const el of host.querySelectorAll('[data-interview-editor]'))if(drafts?.entries().some(v=>v.id==='interview-result-'+el.dataset.interviewEditor)){el.open=true;editors.add(el.dataset.interviewEditor);}}});
    const focusRow=(id,selector)=>{const article=[...host.querySelectorAll('[data-interview-id]')].find(el=>el.dataset.interviewId===id);article?.querySelector(selector)?.focus?.({preventScroll:true});return article;};
    if(!initialInterview)root.requestAnimationFrame?.(()=>{if(!alive())return;root.scrollTo?.({top:saved.scroll||0,behavior:'instant'});if(resume)focusRow(lastInterview,'[data-interview-preparation]');});
    if(initialInterview){const article=focusRow(initialInterview,'[data-interview-application]');if(article){article.classList.add('application-current');article.scrollIntoView({block:'center',behavior:'instant'});}}
  }
  const api={completed,filtered,render,page};if(typeof module==='object'&&module.exports)module.exports=api;root.CampusInterviews=api;return api;
})(typeof window==='undefined'?globalThis:window);
