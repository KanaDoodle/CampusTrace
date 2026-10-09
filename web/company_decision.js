'use strict';
const CampusCompanyDecision=(function(root){
  const states={ANALYZED:'已深度分析',BASIC:'待分析',STALE:'分析待更新'};
  function orderedRows(data){
    const report=data.comparison, ranks=new Map((report.holistic?.choices||[]).map(c=>[c.job_id,c]));
    return (report.jobs||[]).map(row=>({...row,choice:ranks.get(row.job.id)})).sort((a,b)=>(a.choice?.rank??Infinity)-(b.choice?.rank??Infinity)||a.job.title.localeCompare(b.job.title,'zh'));
  }
  function planningState(context,campaigns){
    if(context?.application)return {allowed:false,label:'查看投递进展',reason:''};
    if(!context)return {allowed:false,label:'先刷新投递状态',reason:'投递状态尚未读取。'};
    if(context.current_status==='CLOSED')return {allowed:false,label:'已截止或关闭',reason:'请到官网核对最新状态。'};
    const rule=(campaigns||[]).find(r=>r.id===context.campaign_id);
    if(rule&&rule.remaining<=0)return {allowed:false,label:'本批名额已占用',reason:'可在投递进展中核对或撤回尚未投递的计划。'};
    return {allowed:true,label:'加入投递计划',reason:rule?'使用「'+rule.name+'」的一个计划名额。':'未登记此岗位的限投规则，请先核对官网。'};
  }
  function renderWorkspace(data,{esc,D},pending=''){
    const report=data.comparison,workflow=data.workflow||{},contexts=new Map((workflow.jobs||[]).map(c=>[c.job_id,c])),rules=workflow.campaigns||[],rows=orderedRows(data);
    const choices=report.holistic?.choices||[],ranked=new Set(choices.map(c=>c.job_id)),outside=rows.filter(r=>!ranked.has(r.job.id));
    const rowHTML=row=>{
      const id=row.job.id,c=contexts.get(id),plan=planningState(c,rules),link=root.CampusApplications?.officialLink(c?.official_url)||'',choice=row.choice;
      return `<article class="company-choice ${choice?.rank===1?'company-choice-first':''}"><div class="company-choice-head"><span class="company-rank">${choice?choice.rank:'—'}</span><div><span class="meta">${choice?choice.rank===1?'本次首选':choice.rank===2?'本次备选':'本次顺序':'未参与当前排序'}${c?.application?' · '+esc(D.label('application',c.application.current_state)):''}</span><h3>${esc(row.job.title)}</h3><p class="meta">${esc((row.job.locations||[]).join(' / ')||'地点待核对')} · ${esc(D.label('job',c?.current_status||row.job.current_status))}${c?.deadline?' · 截止 '+esc(c.deadline_date?c.deadline_date+'（当天结束）':D.date(c.deadline)): ' · 截止时间未明确'}</p></div></div>${choice?`<p class="company-choice-reason">${esc(choice.reason)}</p><dl class="company-tradeoffs"><div><dt>相对优势</dt><dd>${esc(choice.advantage)}</dd></div><div><dt>需要取舍</dt><dd>${esc(choice.tradeoff)}</dd></div></dl><details><summary>查看比较依据</summary><blockquote>${esc(choice.job_excerpt)}</blockquote>${(choice.evidence||[]).map(e=>`<blockquote>${esc(e.excerpt)}</blockquote>`).join('')}</details>`:`<p>${esc(states[row.state]||'待核对')}${row.holistic?' · '+esc(root.CampusDecision.fitLabels[row.holistic.fit]):''}。${row.blocked_reason?esc(row.blocked_reason):'单岗结论可作参考，整体比较后才给出同公司顺序。'}</p>`}<div class="actions"><button class="btn btn-small ${plan.allowed?'btn-primary':''}" data-company-plan="${esc(id)}" ${!plan.allowed&&!c?.application?'disabled':''}>${esc(plan.label)}</button><button class="btn btn-small" data-company-detail="${esc(id)}">查看岗位</button>${row.state==='ANALYZED'?`<button class="text-btn" data-company-prepare="${esc(id)}">准备清单</button>`:''}${link?`<a class="text-btn" href="${esc(link)}" target="_blank" rel="noopener noreferrer">招聘官网</a>`:''}</div>${pending===id?`<div class="company-plan-confirm" role="region" aria-label="确认加入计划"><p>将「${esc(row.job.title)}」加入投递计划。${esc(plan.reason)}这里只登记计划，实际投递需在招聘官网完成。</p><button class="btn btn-primary" data-company-plan-confirm="${esc(id)}">确认加入计划</button><button class="btn" data-company-plan-cancel>取消</button></div>`:''}</article>`;
    };
    return `<div class="company-decision-grid"><section class="company-ranking" aria-label="本次岗位取舍"><div class="company-section-heading"><h2>${report.holistic?'这次，优先考虑哪一份？':'先选候选岗位，再比较取舍'}</h2><p>${report.holistic?esc(report.holistic.summary):'已有单岗分析仍可查看。完整材料比较会进一步解释首选与备选。'}</p><p class="meta">${report.scope==='ALL'?'该公司全部本地岗位':'本次手动选择的岗位'} ${report.total} 个 · 当前排序 ${choices.length} 个 · 待分析 ${report.pending} · 分析待更新 ${report.stale}${report.holistic?' · 分析于 '+esc(D.date(report.holistic.analyzed_at)):''}。未覆盖官网全部岗位。</p>${report.holistic_notice?`<p class="pending-note">${esc(report.holistic_notice)}</p>`:''}</div>${rows.filter(r=>ranked.has(r.job.id)).map(rowHTML).join('')}${outside.length?`<details class="company-unranked" ${!choices.length?'open':''}><summary>${choices.length?'未参与排序的岗位':'范围内岗位'} ${outside.length} 个</summary>${outside.map(rowHTML).join('')}</details>`:''}${report.holistic?.questions?.length?`<div class="company-open-questions"><h3>还需确认</h3><ul>${report.holistic.questions.map(q=>`<li>${esc(q)}</li>`).join('')}</ul></div>`:''}</section><aside class="company-action-rail" aria-label="投递名额与已选岗位"><section><h2>这家公司，已经选了什么？</h2>${workflow.applications?.length?`<ul class="company-applications">${workflow.applications.map(a=>{const row=rows.find(r=>r.job.id===a.job_id);return `<li><button class="text-btn" data-company-application="${esc(a.id)}">${esc(a.job?.title||row?.job.title||'范围外的投递记录')}</button><span>${esc(D.label('application',a.current_state))}${a.applied_at?' · 已实际投递':''}</span></li>`;}).join('')}</ul>`:'<p class="meta">还没有这家公司的投递记录。</p>'}<button class="btn btn-small" data-company-applications>管理投递与限投规则</button></section><section><h2>同批次投递名额</h2>${rules.length?rules.map(r=>`<article class="company-quota"><h3>${esc(r.name)}</h3><p><strong>${Number(r.remaining)}</strong> 个剩余名额 / 共 ${Number(r.limit)} 个</p><p class="meta">计划 ${Number(r.planned)} 个 · 已实际投递 ${Number(r.submitted)} 个 · 规则包含 ${r.job_ids.length} 个岗位</p>${r.conflict?'<p class="pending-note">记录超出当前规则，请核对。真实投递记录会保留。</p>':''}<p class="meta">只约束这条规则中选定的岗位；本次缩小比较范围不会释放名额。</p>${root.CampusCampaigns?.safeURL(r.rule_url)?`<a href="${esc(root.CampusCampaigns.safeURL(r.rule_url))}" target="_blank" rel="noopener noreferrer">查看规则来源</a>`:''}</article>`).join(''):'<p class="meta">尚未登记限投规则，不代表官网允许无限投递。</p>'}</section><p class="form-note">投递状态读取于 ${esc(D.date(workflow.as_of))}。确认加入计划时，服务端会再次检查名额；排序仅供取舍参考。</p></aside></div>`;
  }
  async function page(set,heading,{api,esc,D,navigate,active=()=>true,initialCompany=''}){
    const [cap,companies]=await Promise.all([api('/api/profile/resume/capabilities'),api('/api/matching/company-catalog')]);if(!active())return;
    root.CampusModels.bindUser(cap.user_id);root.CampusMatching.bindUser(cap.user_id);
    if(!companies.length){set(heading+'<div class="empty-state"><h2>先收集几份想投的岗位</h2><p>导入招聘来源后，可以在这里比较同公司岗位。</p><button class="btn" id="company-empty">去岗位雷达</button></div>');document.querySelector('#company-empty').onclick=()=>navigate('matching');return;}
    let company=companies.some(c=>c.company===initialCompany)?initialCompany:companies[0].company,catalog=[],selected=new Set(),selectedScope=false,appliedIDs=[],data=null,busy=false,loadError=false,notice='',pending='',search='',catalogPage=1,revision=0;
    const q=s=>document.querySelector(s),identity=()=>root.CampusMatching.matchIdentity();
    let annotationTop='',annotationReason='',annotationReviewed=false,evaluationPayload=null,annotationSaved=JSON.stringify({top:'',reason:'',reviewed:false});
    function scope(){return selectedScope?appliedIDs:[];}
    function annotationSnapshot(){const f=q('#company-eval-form');return JSON.stringify({top:f?f.elements.top.value:annotationTop,reason:f?f.elements.reason.value:annotationReason,reviewed:f?f.elements.reviewed.checked:annotationReviewed});}
    function captureAnnotation(){const form=q('#company-eval-form');if(form){annotationTop=form.elements.top.value;annotationReason=form.elements.reason.value;annotationReviewed=form.elements.reviewed.checked;}}
    function clearAnnotation(){annotationTop=annotationReason='';annotationReviewed=false;evaluationPayload=null;annotationSaved=JSON.stringify({top:'',reason:'',reviewed:false});}
    function picker(){
      const rows=catalog.filter(j=>(j.title+' '+(j.locations||[]).join(' ')).toLowerCase().includes(search.toLowerCase())),shown=rows.slice((catalogPage-1)*30,catalogPage*30);
      return `<p class="meta">已选 ${selected.size} / 16 个。仅勾选不会改变当前报告，点击“查看所选岗位”后更新范围。</p><div class="company-picker-list">${shown.map(j=>`<label class="check"><input type="checkbox" data-company-select="${esc(j.id)}" ${selected.has(j.id)?'checked':''}>${esc(j.title)}<small>${esc((j.locations||[]).join(' / '))}</small></label>`).join('')||'<p class="meta">没有找到岗位，换个关键词试试。</p>'}</div><div class="actions"><button class="btn btn-small" id="company-picker-prev" ${catalogPage===1?'disabled':''}>上一页</button><span class="meta">${catalogPage} / ${Math.max(1,Math.ceil(rows.length/30))} 页 · ${rows.length} 个岗位</span><button class="btn btn-small" id="company-picker-next" ${catalogPage>=Math.ceil(rows.length/30)?'disabled':''}>下一页</button></div>`;
    }
    function bindPicker(){
      for(const el of document.querySelectorAll('[data-company-select]'))el.onchange=()=>{if(el.checked&&selected.size>=16){el.checked=false;notify('每次整体比较最多选择 16 个岗位。');return;}el.checked?selected.add(el.dataset.companySelect):selected.delete(el.dataset.companySelect);q('#company-picker-content').innerHTML=picker();bindPicker();};
      q('#company-picker-prev').onclick=()=>{catalogPage--;q('#company-picker-content').innerHTML=picker();bindPicker();};q('#company-picker-next').onclick=()=>{catalogPage++;q('#company-picker-content').innerHTML=picker();bindPicker();};
    }
    function notify(message){notice=message;const el=q('#company-notice');if(el)el.textContent=message;}
    function draw(){
      if(!active())return;
      const report=data?.comparison;
      if(!set(`${heading}<p class="company-page-intro">把岗位取舍、截止时间和投递名额放在一起。先选值得投的，再记下你的决定。</p><div class="company-decision-toolbar"><label>选择公司<select id="company-switch" ${busy?'disabled':''}>${companies.map(c=>`<option value="${esc(c.company)}" ${company===c.company?'selected':''}>${esc(c.company)}（${c.total}）</option>`).join('')}</select></label><div class="actions"><button class="btn" id="company-refresh" ${busy?'disabled':''}>${busy?'正在读取…':'刷新当前范围'}</button><button class="btn btn-primary" id="company-analyze" ${busy||loadError||!report?.holistic_job_ids?.length||report.holistic_notice?'disabled':''}>${report?.holistic?'核对当前比较资料':'核对资料并生成比较'}</button></div></div><details class="company-picker" ${!data?'open':''}><summary>调整候选岗位 <span class="meta">${catalog.length} 个本地岗位</span></summary><p>可按岗位名称或城市搜索，勾选同批次的候选岗位。整体比较每次最多 16 个，长篇资料还需按完整文字量缩小范围。</p><label>查找候选岗位<input id="company-picker-search" type="search" value="${esc(search)}" placeholder="例如：后端、Agent、上海"></label><div id="company-picker-content">${picker()}</div><div class="actions"><button class="btn btn-primary" id="company-apply-scope" ${busy?'disabled':''}>查看所选岗位</button><button class="btn" id="company-all-scope" ${busy||catalog.length>200?'disabled':''}>查看全部本地岗位</button></div>${catalog.length>200?'<p class="meta">这家公司超过 200 个岗位，请先搜索并选择候选范围。</p>':''}</details><p id="company-notice" role="status" class="pending-note">${esc(notice)}</p><div id="company-workspace" aria-busy="${busy}">${data?renderWorkspace(data,{esc,D},pending):'<div class="empty-state"><h2>选出一组候选岗位</h2><p>查看已有报告不会调用模型；尚无比较时，会先展示核对资料的入口。</p></div>'}</div>${data?`<details class="company-evaluation" ${annotationTop||annotationReason||evaluationPayload?'open':''}><summary>评测这组排序</summary><p>先阅读岗位与当前资料，再标注你认可的首选。这个标注是个人取舍参考，不修改岗位分析，也不代表客观录用概率。</p><form id="company-eval-form"><label>我认可的首选<select name="top" required><option value="">请自行选择，不默认采用模型首选</option>${(report.jobs||[]).filter(j=>(report.holistic_job_ids||[]).includes(j.job.id)).map(j=>`<option value="${esc(j.job.id)}" ${annotationTop===j.job.id?'selected':''}>${esc(j.job.title)}</option>`).join('')}</select></label><label>选择理由<textarea name="reason" required maxlength="2000" placeholder="例如：主要工作与我的 Go 服务项目更相近，算法研究要求较少。">${esc(annotationReason)}</textarea></label><label class="check"><input type="checkbox" name="reviewed" required ${annotationReviewed?'checked':''}>我已阅读这组岗位与求职材料，认可上述参考结论</label><p class="form-note">导出前会展示脱敏后的完整资料；下载和离线评分不会调用模型。跨版本或模型的比较可用项目中的匹配评测工具完成。</p><button class="btn" ${busy||loadError||!report.holistic_input_key||report.holistic_notice?'disabled':''}>预览评测案例</button></form><div id="company-eval-preview">${evaluationPayload?`<h3>核对实际导出的资料</h3><p>包含当前脱敏求职材料、完整岗位原文、人工标注与可复用的比较结果。请检查姓名等敏感文字，再决定下载。</p><details open><summary>查看完整案例文字</summary><pre class="company-eval-document">${esc(JSON.stringify(evaluationPayload,null,2))}</pre></details><button class="btn btn-primary" id="company-eval-download" ${busy||loadError?'disabled':''}>确认下载评测案例</button>`:''}</div></details>`:''}`))return;
      q('#company-switch').onchange=async e=>{if(!await root.CampusNavigation.leave()){e.target.value=company;return;}company=e.target.value;selected.clear();selectedScope=false;appliedIDs=[];search='';catalogPage=1;data=null;pending='';clearAnnotation();await loadCompany();};
      q('#company-picker-search').oninput=e=>{search=e.target.value;catalogPage=1;q('#company-picker-content').innerHTML=picker();bindPicker();};bindPicker();
      q('#company-refresh').onclick=()=>catalog.length?loadWorkspace():loadCompany();
      q('#company-apply-scope').onclick=async()=>{captureAnnotation();if(annotationSnapshot()!==annotationSaved&&!await root.CampusNavigation.confirmDiscard())return;if(!selected.size){notify('请先勾选至少一个候选岗位。');return;}selectedScope=true;appliedIDs=[...selected];data=null;clearAnnotation();loadWorkspace();};
      q('#company-all-scope').onclick=async()=>{captureAnnotation();if(annotationSnapshot()!==annotationSaved&&!await root.CampusNavigation.confirmDiscard())return;selectedScope=false;appliedIDs=[];data=null;clearAnnotation();loadWorkspace();};
      q('#company-analyze').onclick=()=>navigate('matching',{comparison:{company,jobIDs:report.holistic_job_ids,review:true}});
      for(const b of document.querySelectorAll('[data-company-detail]'))b.onclick=()=>navigate('matching',{jobID:b.dataset.companyDetail});
      for(const b of document.querySelectorAll('[data-company-prepare]'))b.onclick=()=>navigate('matching',{jobID:b.dataset.companyPrepare,view:'preparation'});
      for(const b of document.querySelectorAll('[data-company-application]'))b.onclick=()=>navigate('applications',{applicationID:b.dataset.companyApplication});
      for(const b of document.querySelectorAll('[data-company-applications]'))b.onclick=()=>navigate('applications');
      for(const b of document.querySelectorAll('[data-company-plan]'))b.onclick=()=>{const context=data.workflow.jobs.find(c=>c.job_id===b.dataset.companyPlan);if(context?.application){navigate('applications',{applicationID:context.application.id});return;}captureAnnotation();pending=b.dataset.companyPlan;draw();};
      for(const b of document.querySelectorAll('[data-company-plan-cancel]'))b.onclick=()=>{captureAnnotation();pending='';draw();};
      for(const b of document.querySelectorAll('[data-company-plan-confirm]'))b.onclick=async()=>{
        if(busy)return;captureAnnotation();const id=b.dataset.companyPlanConfirm;busy=true;draw();
        try{await api('/api/applications','POST',{job_id:id});pending='';notice='已加入投递计划。';}
        catch(error){notice=error.message;}
        finally{busy=false;if(active())await loadWorkspace(true);}
      };
      if(q('#company-eval-form'))q('#company-eval-form').onsubmit=async e=>{
        e.preventDefault();if(busy||!e.target.reportValidity())return;captureAnnotation();busy=true;draw();
        try{
          const payload=await api('/api/matching/evaluation/export','POST',{...identity(),company,job_ids:report.holistic_job_ids,expected_scope_key:report.holistic_input_key,reference:{acceptable_top_job_ids:[annotationTop],reason:annotationReason,reviewed:annotationReviewed}});
          if(!active())return;
          evaluationPayload=payload;
        }catch(error){if(active())notify(error.message);}
        finally{busy=false;if(active())draw();}
      };
      root.CampusNavigation.register({active,dirty:()=>busy||annotationSnapshot()!==annotationSaved});
      if(q('#company-eval-download'))q('#company-eval-download').onclick=()=>{const payload=evaluationPayload,blob=new Blob([JSON.stringify(payload,null,2)],{type:'application/json;charset=utf-8'}),url=URL.createObjectURL(blob),a=document.createElement('a');a.href=url;a.download='CampusTrace-匹配评测-'+payload.cases[0].id.slice(0,12)+'.json';document.body.append(a);a.click();a.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);const ref=payload.cases[0].reference;annotationSaved=JSON.stringify({top:ref.acceptable_top_job_ids[0],reason:ref.reason,reviewed:ref.reviewed});notify('评测案例已下载。');};
      if(q('#company-eval-form'))q('#company-eval-form').oninput=()=>{evaluationPayload=null;const preview=q('#company-eval-preview');if(preview)preview.replaceChildren();};
      if(busy||loadError){for(const b of document.querySelectorAll('#company-workspace button'))b.disabled=true;for(const el of q('#company-eval-form')?.elements||[])el.disabled=true;}
    }
    async function loadWorkspace(keepNotice=false){
      if(busy||!active())return;if(!selectedScope&&catalog.length>200){notify('请先勾选候选岗位，再查看所选范围。');return;}captureAnnotation();const rev=++revision;busy=true;pending='';if(!keepNotice)notice='';draw();
      try{const result=await api('/api/matching/company-workspace','POST',{...identity(),company,job_ids:scope()});if(!active()||rev!==revision)return;if(data?.comparison.holistic_input_key!==result.comparison.holistic_input_key)clearAnnotation();data=result;loadError=false;}
      catch(error){if(active()&&rev===revision){loadError=true;notice=error.message+(data?' 当前显示上次读取的报告，标注已保留；请刷新后再操作。':'');}}
      finally{if(active()&&rev===revision){busy=false;draw();}}
    }
    async function loadCompany(){
      const rev=++revision;busy=true;draw();
      try{const rows=await api('/api/matching/company-catalog?company='+encodeURIComponent(company));if(!active()||rev!==revision)return;catalog=rows;}
      catch(error){if(active()&&rev===revision){catalog=[];notice=error.message;}}
      finally{if(active()&&rev===revision){busy=false;draw();}}
      if(active()&&catalog.length&&catalog.length<=200)await loadWorkspace();
    }
    await loadCompany();
  }
  const api={page,renderWorkspace,orderedRows,planningState};if(typeof module==='object'&&module.exports)module.exports=api;root.CampusCompanyDecision=api;return api;
})(typeof window==='undefined'?globalThis:window);
