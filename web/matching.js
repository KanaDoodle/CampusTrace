'use strict';
const CampusMatching=(function(root){
  const labels={BASIC:'仅完成初筛',ANALYZED:'已深度分析',STALE:'待更新',QUALIFICATION:'投递资格',REQUIRED:'必需能力',BONUS:'加分项',RESPONSIBILITY:'工作内容',DIRECT:'直接匹配',PARTIAL:'部分匹配',TRANSFERABLE:'可迁移经验',NO_EVIDENCE:'暂无依据',MISMATCH:'明确不符合',SKILL:'技能',LANGUAGE:'语言',IMPLEMENTED:'已确认项目事实',LIMITATION:'已确认局限',DEGREE:'学历',GRADUATION:'毕业届别',EXPERIENCE:'相关经验',MAJOR:'专业',ROLE:'意向职能',CITY_PREFERRED:'首选城市',CITY_ACCEPTABLE:'可接受城市',JOB_TYPE_PREFERENCE:'意向岗位类型'};
  const tiers={HIGH:'优先查看',POSSIBLE:'可能相关',UNCERTAIN:'信息不足',LOW:'暂不优先'};
  const workflows={UNHANDLED:'未处理',SAVED:'稍后看',PLANNED:'已计划',APPLIED:'已投递',IGNORED:'已忽略',ENDED:'已结束'};
  const directions={MATCH:'主投方向',RELATED:'相关方向',UNCERTAIN:'待判断',UNRELATED:'明显无关'};
  const directionOf=row=>row.local?.direction?.status||'UNCERTAIN';
  function directionMatch(row,value){return !value||value==='ALL'||(value==='MAIN'?['MATCH','RELATED'].includes(directionOf(row)):directionOf(row)===value);}
  function catalogVisible(row,workflow,showIgnored=false){return row.disposition!=='IGNORED'||showIgnored||workflow==='IGNORED';}
  function bulkTargets(rows,value){return rows.filter(row=>value==='NONE'?row.disposition==='IGNORED':!['IGNORED','SAVED'].includes(row.disposition)&&!row.application);}
  function workflowMatch(row,value){
    const a=row.application,state=a?.current_state;
    if(!value)return true;
    if(value==='UNHANDLED')return !a&&!['SAVED','IGNORED'].includes(row.disposition);
    if(value==='SAVED'||value==='IGNORED')return row.disposition===value;
    if(value==='PLANNED')return state==='PLANNED';
    if(value==='APPLIED')return !!a&&(!!a.applied_at||['APPLIED','OA','INTERVIEW','HR','OFFER'].includes(state));
    if(value==='ENDED')return ['OFFER','REJECTED','WITHDRAWN'].includes(state);
    return false;
  }
  function workflowHTML(row,{esc,D}){
    const a=row.application,mark=['SAVED','IGNORED'].includes(row.disposition)?workflows[row.disposition]:'';
    const stage=a?(a.current_state==='PLANNED'?'已计划':D.label('application',a.current_state)):mark||'未处理';
    return `<span class="job-workflow"><span>${esc(stage)}</span>${a&&mark?`<span>${esc(mark)}</span>`:''}</span>`;
  }
  let maskName='',boundUser='';
  function bindUser(id){if(id!==boundUser){maskName='';boundUser=id;}}
  function lock(){maskName='';boundUser='';}
  function identity(){const m=root.CampusModels.requestConfig();return {model_url:m?.url||'',model_name:m?.model||'',mask_name:maskName};}
  function eligible(j){return !j.excluded_reason&&j.state!=='ANALYZED';}
  function drawerActions(row,{application,applicationReady,loading,officialURL},{esc,D,U}){
    const disposition=row.disposition||'NONE';
    const planLabel=application?'查看投递进展':loading?'读取投递状态…':applicationReady?'加入投递计划':'重试读取投递状态';
    return `${application?`<p class="drawer-workflow"><span class="badge badge-green">${esc(D.label('application',application.current_state))}</span><span>已建立投递记录</span></p>`:''}<div class="drawer-actions"><button class="btn btn-primary" data-detail-analyze ${eligible(row)?'':'disabled'}>${U.icon('spark')}${row.state==='ANALYZED'?'已深度分析':row.state==='STALE'?'更新分析':'深度分析'}</button><button class="btn" data-detail-plan ${loading?'disabled':''}>${U.icon(application?'list':'plus')}${planLabel}</button><button class="btn" data-detail-preparation>准备清单</button>${officialURL?`<a class="btn" href="${esc(officialURL)}" target="_blank" rel="noopener noreferrer" aria-label="在新标签页查看官网岗位">${U.icon('external')}查看官网岗位</a>`:''}</div>${!loading&&!officialURL?'<p class="meta">暂未记录岗位网页链接</p>':''}<div class="drawer-secondary-actions"><button class="btn btn-subtle btn-small" data-detail-pref="SAVED" aria-pressed="${disposition==='SAVED'}">${U.icon('bookmark')}${disposition==='SAVED'?'取消稍后看':'稍后看'}</button><button class="btn btn-subtle btn-small" data-detail-pref="IGNORED" aria-pressed="${disposition==='IGNORED'}">${disposition==='IGNORED'?'取消忽略':'忽略'}</button></div>`;
  }
  function pack(rows,max=3,bytes=24000){
    const batch=[];let size=0;
    for(const row of rows){if(batch.length===max||size+row.text_bytes>bytes)break;batch.push(row);size+=row.text_bytes;}
    return batch;
  }
  function shortlist(rows,limit){return rows.filter(eligible).slice(0,limit);}
  function filtered(rows,query,state,tier=''){
    const normalize=s=>String(s||'').trim().toLowerCase().replace(/服务端|服务器端|backend|server-side/g,'后端');const q=normalize(query);
    return rows.filter(row=>(!q||normalize(row.job.company+' '+row.job.title+' '+(row.local?.role||'')).includes(q))&&(!state||row.state===state)&&(!tier||(row.local?.tier||'UNCERTAIN')===tier));
  }
  function journeyHTML(jobs,{esc},hasDirection,workflow){
    const visible=jobs.filter(j=>j.disposition!=='IGNORED');
    const stages=[['discover','相关机会',visible.filter(j=>!hasDirection||directionMatch(j,'MAIN')).length],['saved','稍后考虑',visible.filter(j=>workflowMatch(j,'SAVED')).length],['planned','准备投递',visible.filter(j=>workflowMatch(j,'PLANNED')).length],['applied','已投递',visible.filter(j=>workflowMatch(j,'APPLIED')).length]];
    const selected={SAVED:'saved',PLANNED:'planned',APPLIED:'applied'}[workflow]||'discover';
    return `<nav class="workbench-journey" aria-label="求职进度筛选">${stages.map(([key,label,count])=>`<button data-match-journey="${key}" aria-pressed="${selected===key}"><span>${esc(label)}</span><strong>${count}</strong></button>`).join('')}</nav>`;
  }
  function jobRowHTML(j,{esc,D},selected,current,disabled,failed){
    const core=(j.breakdown||[]).find(s=>s.category==='REQUIRED');
    const analyzed=j.state==='ANALYZED',id=esc(j.job.id),score=analyzed&&Number.isFinite(j.score)?Number(j.score).toFixed(1):'—';
    const scoreLabel=analyzed?(Number.isFinite(j.score)?'核心技术匹配':'依据不足'):j.state==='STALE'?'分析待更新':'待深度分析';
    const reason=j.excluded_reason||j.local?.reasons?.[0]||j.local?.direction?.reason||'暂无足够资料线索，可查看原文核对。';
    return `<article class="workbench-job ${selected?'is-selected ':''}${current?'is-current':''}" data-workbench-row="${id}"><label class="match-row-select"><input type="checkbox" data-match-select="${id}" aria-label="${esc('选择 '+D.text(j.job.company)+' '+D.text(j.job.title))}" ${selected?'checked':''} ${disabled?'disabled':''}></label><div class="workbench-job-main"><button class="job-title" data-match-job="${id}" ${current?'aria-current="true"':''}>${esc(D.text(j.job.title))}</button><p class="job-meta">${esc(D.text(j.job.company))} · ${esc((j.job.locations||[]).map(D.text).join(' / ')||'地点待确认')} · ${esc(D.label('job_type',j.job.job_type))}</p><p class="workbench-reason">${esc(reason)}</p><div class="workbench-job-notes">${workflowHTML(j,{esc,D})}<span>${esc(directions[directionOf(j)]||'待判断')}</span><span class="workbench-tier">${esc(tiers[j.local?.tier]||'信息不足')}</span>${failed?'<span class="workbench-warning">分析失败</span>':''}</div></div><div class="workbench-job-score"><strong>${score}</strong><span>${scoreLabel}</span>${analyzed?`<small>依据 ${Number(j.coverage||0).toFixed(0)}%</small>${core?`<small>${Number(core.direct)||0} 直接 · ${Number(core.partial)||0} 部分</small>`:''}`:''}</div></article>`;
  }
  function evidenceFor(r,m){
    const facts=new Map((r.candidate_facts||[]).map(f=>[f.id,f])),seen=new Set();
    return (m?.evidence||[]).filter(e=>{const f=facts.get(e.id),key=['SKILL','LANGUAGE'].includes(f?.kind)?'skill:'+String(e.excerpt).trim().toLowerCase():e.id+'\n'+e.excerpt;if(seen.has(key))return false;seen.add(key);return true;}).map((e,i)=>({e,i,rank:facts.get(e.id)?.kind==='IMPLEMENTED'?0:1})).sort((a,b)=>a.rank-b.rank||a.i-b.i).map(x=>x.e);
  }
  function structureHTML(r,{esc}){
    const labels={REQUIRED:'核心技术要求',RESPONSIBILITY:'工作内容相关性',BONUS:'加分项'};
    const sections=(r.breakdown||[]).filter(s=>Object.hasOwn(labels,s.category)&&s.total);
    return `<section class="match-result-summary"><h4>这份岗位的匹配结构</h4>${sections.map(s=>`<p><strong>${labels[s.category]}</strong>：直接 ${Number(s.direct)||0} · 部分 ${Number(s.partial)||0} · 可迁移 ${Number(s.transferable)||0} · 暂无依据 ${Number(s.missing)||0}${s.mismatch?' · 明确不符合 '+Number(s.mismatch):''}</p>`).join('')}<p class="form-note">条目有依据可能只是部分匹配，依据覆盖 100% 不等于全部能力已证明。软性要求不计入技术分，可作为面试事例准备。</p></section>`;
  }
  function overviewHTML(v,row,{esc,D,U}){
    if(v.state!=='ANALYZED'||!v.result){
      return `<section class="workbench-assessment"><h3>${v.state==='STALE'?'分析依据已有变化':'先核对方向，再深入分析'}</h3><p>${v.state==='STALE'?'上次分析暂不用于当前判断，可核对变化后更新。':'下面是本地初筛线索，尚未进行模型深度分析。'}</p></section><section class="workbench-next"><h3>${v.state==='STALE'?'下一步，更新这份岗位的分析':'下一步，核对原文后开始分析'}</h3><p>先确认岗位方向与求职条件。发起分析时会展示完整外发资料，确认后才调用模型。</p><button class="btn btn-primary" data-overview-analyze ${eligible(row)?'':'disabled'}>${U.icon('spark')}${v.state==='STALE'?'更新分析':'深度分析'}</button><button class="btn" data-overview-plan>加入投递计划</button></section><details data-remember id="drawer-local" open><summary>本地初筛依据</summary>${renderLocal(row,{esc})}</details>${v.state==='STALE'?`<details><summary>上次深度分析（已过期）</summary>${renderResult(v,{esc,D})}</details>`:''}`;
    }
    const r=v.result,core=(r.breakdown||[]).find(s=>s.category==='REQUIRED'),byID=new Map((r.matches||[]).map(m=>[m.requirement_id,m]));
    // Only current technical matches become highlights. Qualification, intention,
    // and revoked evidence stay in the complete report rather than as strengths.
    const technical=(r.requirements||[]).filter(req=>req.category==='REQUIRED'&&req.aspect!=='SOFT').map(req=>({req,match:byID.get(req.id)}));
    const strengths=technical.filter(({match:m})=>m?.result==='DIRECT'&&m.review_note!=='INVALID_ABILITY_EVIDENCE'&&m.evidence?.length).slice(0,2);
    const gaps=technical.filter(({match:m})=>!m||['NO_EVIDENCE','MISMATCH','PARTIAL','TRANSFERABLE'].includes(m.result)||m.review_note==='INVALID_ABILITY_EVIDENCE').slice(0,2);
    const excerpt=text=>{const value=String(text||'');return value.length>160?value.slice(0,160)+'…':value;};
    const rows=[...strengths.map(item=>({...item,positive:true})),...gaps.map(item=>({...item,positive:false}))];
    const reviewed=(r.matches||[]).filter(m=>['INVALID_ABILITY_EVIDENCE','EXPERIENCE_UNCONFIRMED','CONTEXT_UNCONFIRMED'].includes(m.review_note)).length;
    const score=Number.isFinite(r.score)?Number(r.score).toFixed(1):'暂无可靠评分';
    return `<section class="workbench-assessment"><div class="workbench-assessment-head"><span class="workbench-assessment-icon">${U.icon(Number.isFinite(r.score)?'check':'search')}</span><div><h3>${Number.isFinite(r.score)?'核心技术匹配度 '+score+' / 100':'核心要求依据不足，先补充核对'}</h3><p>${r.source==='CHATGPT_IMPORT'?'来源：ChatGPT 聊天导入 · ':''}核心依据覆盖 ${Number(r.coverage||0).toFixed(1)}%${core?` · ${Number(core.known)||0} / ${Number(core.total)||0} 项有依据`:''}</p></div></div><p>投递资格：${esc(D.label('eligibility',r.qualifications?.status))}。匹配分用于辅助比较，不能代替原文与经历核对。</p>${reviewed?`<p class="pending-note">${reviewed} 项结论已本地复核，请在逐项依据中核对。</p>`:''}${r.locally_refreshed?'<p class="form-note">资格与偏好已按当前资料更新，复用原有能力分析。</p>':''}</section>${structureHTML(r,{esc})}<section class="workbench-highlights"><h3>匹配依据与待核实项</h3>${rows.length?rows.map(({req,match:m,positive})=>`<div class="workbench-evidence-row ${positive?'positive':'needs-review'}"><span>${U.icon(positive?'check':'warning')}</span><div><strong>${esc(req.text)}</strong><p>${esc(positive?excerpt(evidenceFor(r,m)[0].excerpt):m?.review_note==='INVALID_ABILITY_EVIDENCE'?'误用的能力引用已撤销，需要补充真实经历。':m?.explanation||'当前资料暂无足够依据，先核实真实经历。')}</p><small>${esc(positive?'直接匹配':labels[m?.review_note==='INVALID_ABILITY_EVIDENCE'?'NO_EVIDENCE':m?.result]||'暂无依据')}</small></div></div>`).join(''):'<p class="meta">暂无可摘要展示的核心技术要求，请展开完整报告核对。</p>'}<button class="text-btn" data-overview-evidence>查看全部 ${r.requirements?.length||0} 项要求与依据</button></section><section class="workbench-next"><h3>下一步，把分析变成准备行动</h3><p>${gaps.length?'先核对上面的技术差距，再准备已有项目的具体讲解。暂无依据不代表不会。':'从准备清单整理项目讲解与岗位关注点，再安排投递。'}</p><div class="actions"><button class="btn btn-primary" data-overview-plan>${U.icon('plus')}加入投递计划</button><button class="btn" data-overview-preparation>查看准备清单</button></div></section><details data-remember id="drawer-full-report"><summary>完整分析与评分口径</summary>${renderResult(v,{esc,D})}</details><details data-remember id="drawer-local"><summary>本地初筛依据</summary>${renderLocal(row,{esc})}</details>`;
  }
  function renderLocal(row,{esc}){
    const v=row.local;
    if(!v)return `<p class="empty">${esc(row.excluded_reason||'岗位原文尚不可用，暂无法提供本地初筛依据。')}</p>`;
    const result={SIGNAL:'有资料线索',PARTIAL:'部分有线索',NO_EVIDENCE:'资料中暂无依据'};
    const direction=v.direction?`<p><strong>方向相关性：${esc(directions[v.direction.status]||'待判断')}</strong> · ${esc((v.direction.roles||[]).join('、'))}</p><p>${esc(v.direction.reason)}</p><details><summary>查看方向判断依据</summary>${(v.direction.evidence||[]).map(e=>`<p class="meta">${e.source==='TITLE'?'岗位标题':'岗位职责'}</p><blockquote>${esc(e.excerpt)}</blockquote>`).join('')||'<p>暂缺明确方向依据。</p>'}<p class="meta">方向判断独立于技能得分与城市偏好，不代表能力匹配或投递资格。</p></details>`:'';
    return `<h4>本地初筛依据</h4>${direction}<p><strong>${esc(tiers[v.tier]||'信息不足')}</strong> · 优先级 ${Number(v.score).toFixed(1)} / 100</p><details><summary>查看初筛计算方式</summary><p class="meta">方向最高 30 分，城市与岗位类型偏好 20 分，技术线索 30 分，已确认的项目实现依据 20 分。必需项权重 3，工作内容 2，加分项 1。此处只核对文字线索，不代表掌握程度或录用概率；更新时间仅用于同分排序。</p></details>${v.role?`<p>识别方向：${esc(v.role)} · ${v.role_source==='BODY'?'来自岗位职责':'来自岗位标题'}</p><blockquote>${esc(v.role_excerpt)}</blockquote>`:''}<ul>${(v.reasons||[]).map(r=>`<li>${esc(r)}</li>`).join('')}</ul>${row.excluded_reason?`<p class="pending-note">${esc(row.excluded_reason)}</p>`:''}${(v.warnings||[]).map(w=>`<p class="pending-note">${esc(w)}</p>`).join('')}${v.checks?.length?`<details><summary>核对技术与项目线索（${v.checks.length} 项）</summary><div class="table-wrap"><table><thead><tr><th>岗位要求与原文</th><th>我的资料依据</th><th>初筛结果</th></tr></thead><tbody>${v.checks.map(c=>`<tr><td><span class="pill">${esc(labels[c.category]||c.category)}</span><p>${esc(c.terms.join(c.mode==='ANY'?' / ':' + '))} · ${c.mode==='ANY'?'任选一项':'逐项核对'}</p><blockquote>${esc(c.excerpt)}</blockquote></td><td>${c.evidence?.length?c.evidence.map(e=>`<p><small>${esc(labels[e.kind]||'资料依据')}</small><br>${esc(e.excerpt)}</p>`).join(''):'资料中暂无依据，可补充求职资料后核对'}</td><td>${esc(result[c.result]||'资料中暂无依据')}</td></tr>`).join('')}</tbody></table></div></details>`:''}`;
  }
  function progressKey(user){return 'campustrace:match-progress:v1:'+user;}
  function readProgress(user,hash,model){
    try{const v=JSON.parse(root.sessionStorage.getItem(progressKey(user))||'null');if(v?.hash===hash&&v.model===model&&Array.isArray(v.pending)&&Array.isArray(v.failed))return v;}catch{}
    return {hash,model,pending:[],failed:[],done:0,calls:0};
  }
  function storeProgress(user,v){try{root.sessionStorage.setItem(progressKey(user),JSON.stringify(v));}catch{}}
  function reconcileProgress(progress,jobs){
    const byID=new Map(jobs.map(j=>[j.job.id,j])),completed=new Set();
    const keep=id=>{const job=byID.get(id);if(job?.state==='ANALYZED')completed.add(id);return job&&job.state!=='ANALYZED';};
    progress.pending=[...new Set(progress.pending)].filter(keep);
    const seen=new Set(progress.pending);
    progress.failed=progress.failed.filter(f=>{if(seen.has(f.id)||!keep(f.id))return false;seen.add(f.id);return true;});
    progress.done=(progress.done||0)+completed.size;
    progress.calls=progress.calls||0;
    return progress;
  }
  function queueWork(progress,ids,continuing=false){
    const requested=new Set(ids),hasWork=progress.pending.length||progress.failed.length;
    return {...progress,pending:[...requested,...progress.pending.filter(id=>!requested.has(id))],failed:progress.failed.filter(f=>!requested.has(f.id)),done:continuing||hasWork?progress.done:0,calls:continuing||hasWork?progress.calls:0};
  }
  function analysisActions(progress,options){
    const {running,preparing,authorized,modelAvailable,callsToday,dailyCalls,runCount,pendingCount,failedCount}=options;
    const reason=running?'本轮正在分析，请等待当前批次完成或暂停后续分析。':preparing?'正在准备分析包，请等待完成。':!modelAvailable?'请先在模型设置填写密钥并选择模型。':!authorized?'请先核对上方「本轮外发资料」，并勾选同意发送。':callsToday>=dailyCalls?'今日调用已达到上限，请在北京时间次日重置后继续，或调整每日上限。':'';
    return {reason,run:{disabled:!!reason||!runCount,reason:runCount?reason:'当前筛选结果没有待分析且可分析的岗位。'},continue:{disabled:!!reason||!pendingCount,reason:pendingCount?reason:progress.pending.length?'本轮剩余岗位暂不可分析，请核对岗位状态。':'本轮没有未完成岗位。'},retry:{disabled:!!reason||!failedCount,reason:failedCount?reason:progress.failed.length?'失败项暂不可分析，请核对岗位状态。':'本轮暂无失败项。'}};
  }
  function renderRequirements(r,{esc},allowSupplement=false){
    const byID=new Map((r.matches||[]).map(m=>[m.requirement_id,m]));
    const candidate=new Map((r.candidate_facts||[]).map(f=>[f.id,f]));
    if(!r.requirements?.length)return '<p class="empty">模型未提取到明确要求，因此没有生成能力评分。</p>';
    return `<div class="table-wrap"><table><thead><tr><th>岗位要求与原文</th><th>我的依据</th><th>匹配结论</th></tr></thead><tbody>${r.requirements.map(req=>{const m=byID.get(req.id);return `<tr><td><span class="pill">${esc(req.aspect==='SOFT'?'软性要求（不计技术分）':labels[req.category])}</span>${req.group_id?'<span class="pill">同组任选方向</span>':''}<p>${esc(req.text)}</p><blockquote>${esc(req.excerpt)}</blockquote>${req.graduation_window?`<p class="form-note">毕业范围：${esc(req.graduation_window.from)} 至 ${esc(req.graduation_window.to)}</p><blockquote>${esc(req.graduation_window.excerpt)}</blockquote>`:''}${req.group_id?`<small>选择规则：${esc(req.group_excerpt)}；同组按最有依据的方向核对。</small>`:''}${req.confidence<.8?'<small>要求解释的置信度较低，需核对原文。</small>':''}</td><td>${m?.evidence?.length?evidenceFor(r,m).map(e=>{const f=candidate.get(e.id);return `<p><small>${f?.project_name?esc(f.project_name)+' · ':''}${esc(labels[f?.kind]||'资料依据')}</small><br>${esc(e.excerpt)}</p>`;}).join(''):'资料中暂无足够依据'}</td><td>${m?.review_note==='INVALID_ABILITY_EVIDENCE'?'<span class="pill">错误引用已撤销</span>':m?.review_note==='EXPERIENCE_UNCONFIRMED'?'<span class="pill">技能相关 · 实践待确认</span>':m?.review_note==='CONTEXT_UNCONFIRMED'?'<span class="pill">程度待确认</span>':''}<b>${esc(labels[m?.result]||'暂无依据')}</b><p>${esc(m?.explanation||'需要补充资料后核对。')}</p>${allowSupplement&&req.category!=='QUALIFICATION'&&m?.result==='NO_EVIDENCE'?`<button class="btn btn-small" data-supplement="${esc(req.id)}">补充项目依据</button>`:''}</td></tr>`;}).join('')}</tbody></table></div>`;
  }
  function renderResult(v,helpers){
    const {esc,D}=helpers;
    if(v.state!=='ANALYZED'||!v.result){
      if(v.state==='STALE'&&v.result)return `<p class="pending-note">分析规则、岗位、资料或模型选择已有变化，请重新分析。</p><details><summary>查看上次分析依据（已过期）</summary><p class="meta">分析于 ${esc(D.date(v.result.analyzed_at))}；旧结论保留供核对，不参与当前评分。</p>${renderRequirements(v.result,helpers)}</details>`;
      return `<p class="empty">${v.state==='STALE'?'分析规则、岗位、资料或模型选择已有变化，请重新分析。':'尚未深度分析，可在岗位匹配中查看本地初筛并加入分析。'}</p>`;
    }
    const reviews=(v.result.matches||[]).filter(m=>['INVALID_ABILITY_EVIDENCE','EXPERIENCE_UNCONFIRMED','CONTEXT_UNCONFIRMED'].includes(m.review_note)).length;
    const reviewNotice=reviews?`<p class="pending-note">${reviews} 项结论已本地复核：误用偏好或局限的引用已撤销，仅有技能自述的实践结论保留部分匹配。请查看逐项说明；不代表不能投递。</p>`:'';
    const r=v.result,sections=r.breakdown||[],core=sections.find(s=>s.category==='REQUIRED');
    const sectionLabels={REQUIRED:'核心技术要求',RESPONSIBILITY:'工作内容相关性',BONUS:'加分项',SOFT:'软性要求'};
    const score=r.score==null?'暂无法可靠评分':Number(r.score).toFixed(1)+' / 100';
    const coreNote=core?core.total?`核心技术要求共 ${core.total} 项：直接匹配 ${core.direct} 项，部分匹配 ${core.partial} 项，可迁移经验 ${core.transferable} 项，暂无充分依据 ${core.missing} 项，明确不符合 ${core.mismatch} 项。`:'岗位未提取到可独立核对的核心技术要求。':'';
    const summary=sections.length?`<section class="match-result-summary"><h4>分析摘要</h4><p>${esc(coreNote)}</p><p>投递资格：${esc(D.label('eligibility',r.qualifications?.status))}。城市与岗位类型按已保存偏好核对。</p>${core?.missing?'<p class="pending-note">可补充核心要求相关的具体项目事实，再重新核对；暂无依据不代表你不会。</p>':''}<details><summary>查看各部分的依据覆盖度</summary><div class="table-wrap"><table><thead><tr><th>核对部分</th><th>有依据／总项数</th><th>依据覆盖度</th></tr></thead><tbody>${sections.map(s=>`<tr><td>${esc(sectionLabels[s.category]||s.category)}</td><td>${s.known} / ${s.total}</td><td>${s.total?Number(s.coverage).toFixed(1)+'%':'暂无条目'}</td></tr>`).join('')}</tbody></table></div></details></section>`:'';
    return `<p class="match-score">核心技术匹配度：<strong>${esc(score)}</strong> · 核心条目依据覆盖度 ${Number(r.coverage).toFixed(1)}%</p><details><summary>评分口径与模型信息</summary><p class="meta">主分数只计算有依据的必需技术要求：直接匹配 1、部分匹配 0.5、可迁移经验 0.25、明确不符合 0。核心覆盖不足 60% 时不显示分数。工作内容、加分项、软性要求单独展示；明确任选的同组方向只计一项。分数不代表录用概率，也不改变招聘状态核验结果。</p><p class="meta">分析于 ${esc(D.date(r.analyzed_at))} · ${esc(r.model.split('\n').pop()||'所选模型')}</p></details>${r.locally_refreshed?'<p class="form-note">已按当前资料在本地更新资格与偏好，复用原有能力分析；本次没有调用模型。</p>':''}${reviewNotice}${structureHTML(r,{esc})}${summary}<details><summary>查看全部岗位要求与个人依据（${r.requirements?.length||0} 项）</summary>${renderRequirements(r,helpers,true)}</details><details><summary>查看当前资料的资格核对</summary><p>根据明确条件与已保存资料计算；城市名称已归一，优先专业不作为硬门槛，要求缺失或冲突仍待核验。</p><div class="table-wrap"><table><thead><tr><th>核对项</th><th>岗位要求</th><th>我的情况</th><th>结果</th></tr></thead><tbody>${(r.qualifications?.results||[]).map(row=>`<tr><td>${esc(D.label('evidence',row.rule))}</td><td>${esc(D.requirement(row.requirement,row.rule))}</td><td>${esc(D.requirement(row.candidate_value,row.rule,'candidate_value'))}</td><td>${esc(D.label('rule',row.result))}${row.result==='UNKNOWN'&&/[\u4e00-\u9fff]/.test(row.explanation||'')?`<p class="form-note">${esc(row.explanation)}</p>`:''}</td></tr>`).join('')}</tbody></table></div></details>`;
  }
  async function showJob(box,id,helpers){
    const {api,navigate}=helpers;
    const cap=await api('/api/profile/resume/capabilities');root.CampusModels.bindUser(cap.user_id);bindUser(cap.user_id);
    const result=await api('/api/matching/results/'+encodeURIComponent(id),'POST',identity());
    if(!box.isConnected)return;
    box.innerHTML=`<h3>技能与项目匹配</h3>${renderLocal(result,helpers)}<h4>模型深度分析</h4>${renderResult(result,helpers)}<button type="button" data-open-matching>前往岗位匹配</button>`;
    box.querySelector('[data-open-matching]').onclick=()=>navigate('matching');
    for(const b of box.querySelectorAll('[data-supplement]'))b.onclick=()=>navigate('profile',{evidence:{jobID:id,requirementID:b.dataset.supplement,identity:identity()}});
  }
  async function page(set,heading,{api,esc,D,UserError,navigate,openRecord,initialQuery='',initialJob='',initialView='overview',initialAnalyze=false,initialTask='',initialWorkflow='',active}){
    const cap=await api('/api/profile/resume/capabilities');root.CampusModels.bindUser(cap.user_id);bindUser(cap.user_id);
    let snapshot;try{snapshot=await api('/api/matching/preview','POST',identity());}catch(error){if(error.code!=='MATCH_PROFILE_REQUIRED')throw error;const inventory=await api('/api/jobs');if(!set(root.CampusUI.heading('岗位库','完善求职资料后，可按你的条件初筛与分析。')+'<div class="note-box"><p>先保存求职条件与技能，再开启岗位匹配。</p><button id="match-create-profile" class="btn btn-primary">完善求职资料</button></div>'+inventory.map(j=>`<article class="card"><h3>${esc(D.text(j.title))}</h3><p class="meta">${esc(D.text(j.company))} · ${esc((j.locations||[]).join('、'))}</p><button data-basic-job="${esc(j.id)}" class="btn">查看核验记录</button></article>`).join('')))return;document.querySelector('#match-create-profile').onclick=()=>navigate('profile');for(const b of document.querySelectorAll('[data-basic-job]'))b.onclick=()=>openRecord(b.dataset.basicJob).catch(err=>{document.querySelector('#notice').textContent=err.message;});return;}
    const Navigation=root.CampusNavigation,browse=Navigation?.readBrowse(cap.user_id)||{};
    let query=initialQuery||browse.query||'',filter=initialQuery?'':browse.filter||'',tierFilter=initialQuery?'':browse.tier||'',sort=browse.sort||'local',pageNo=initialQuery?1:browse.page||1,running=false,paused=false,consentHash='',notice='';
    const C=root.CampusMatchingChat,U=root.CampusUI,Decision=root.CampusDecision;
    let companyFilter=initialQuery?'':browse.company||'',cityFilter=initialQuery?'':browse.city||'',panel='',intent=null,reviewBundle=null,reviewBusy=false,reviewError='',reviewRevision=0,searchTimer;
    let workflowFilter=Object.hasOwn(workflows,initialWorkflow)?initialWorkflow:initialQuery?'':browse.workflow||'';
    const targetRoles=()=>snapshot.candidate.facts.filter(f=>f.kind==='ROLE').map(f=>f.text);
    let directionFilter=initialQuery||initialWorkflow?'ALL':browse.direction||(targetRoles().length?'MAIN':'ALL'),showIgnored=!!browse.showIgnored,bulkBusy=false;
    let lastJob=browse.lastJob||'',lastTab=browse.lastTab||'overview',drawerOpen=!!browse.drawerOpen,lastOpened=null;
    let comparisonCompany='',comparisonScope='ALL',comparisonReport=null,comparisonBusy=false,comparisonError='',comparisonRevision=0;
    let reviewKeys=new Map(),renderedViewKey='';
    let importText='',importDocument=null,importPreview=null,importError='',importBusy=false,importRevision=0,importMask=maskName,importOrigins=new Map();
    const selected=C.readSelection(cap.user_id,snapshot.jobs);
    let onlySelected=initialQuery?false:!!browse.onlySelected,preparing=false,exportFiles=[],exportIndex=0,exportConsent=false,exportKeys=new Map();
    const modelKey=()=>JSON.stringify([identity().model_url,identity().model_name,cap.model]);
    let progress=reconcileProgress(readProgress(cap.user_id,snapshot.candidate_hash,modelKey()),snapshot.jobs);
    const Tasks=root.CampusMatchTasks,durable=!!cap.durable_matching&&!!Tasks;
    let taskRuns=durable?await api('/api/matching/tasks'):[],task=taskRuns.find(Tasks?.active||(()=>false))||taskRuns[0]||null;
    let taskNotice='';
    if(initialTask&&durable){
      try{const target=taskRuns.find(v=>v.id===initialTask)||await api('/api/matching/tasks/'+encodeURIComponent(initialTask));task=target;if(!taskRuns.some(v=>v.id===target.id))taskRuns.push(target);}
      catch{taskNotice='这条分析任务暂不可用，请查看最近任务或刷新。';}
      query=filter=tierFilter=companyFilter=cityFilter=workflowFilter='';onlySelected=false;pageNo=1;drawerOpen=false;
      directionFilter='ALL';showIgnored=true;
    }
    function applyTask(v){task=v;const i=taskRuns.findIndex(x=>x.id===v.id);if(i>=0)taskRuns[i]=v;else taskRuns.unshift(v);taskRuns=taskRuns.slice(0,20);progress={hash:snapshot.candidate_hash,model:modelKey(),...Tasks.progress(v,D)};running=Tasks.active(v);paused=!running;}
    if(task)applyTask(task);if(initialTask&&task?.id===initialTask&&progress.failed.length)filter='FAILED';storeProgress(cap.user_id,progress);
    if(taskNotice)notice=taskNotice;
    const baseline=new Map(snapshot.jobs.map(j=>[j.job.id,j.input_key]));let autoQueue=[];
    const current=()=>active();
    function saveBrowse(){Navigation?.storeBrowse(cap.user_id,{query,filter,tier:tierFilter,workflow:workflowFilter,direction:directionFilter,showIgnored,sort,page:pageNo,company:companyFilter,city:cityFilter,onlySelected,lastJob,lastTab,drawerOpen});}
    function checkpoint(){
      const search=document.querySelector('#match-search');
      if(search&&search.value!==query){query=search.value;pageNo=1;}
      saveBrowse();
    }
    function ordered(data){return sort==='deep'?[...data].sort((a,b)=>(b.state==='ANALYZED'?b.score??-1:-1)-(a.state==='ANALYZED'?a.score??-1:-1)||b.preliminary_score-a.preliminary_score):data;}
    const cityName=s=>String(s||'').replace(/市$/,'');
    function rows(direction=directionFilter){return ordered(filtered(snapshot.jobs,query,filter==='FAILED'?'':filter,tierFilter).filter(j=>catalogVisible(j,workflowFilter,showIgnored)&&directionMatch(j,direction)&&workflowMatch(j,workflowFilter)&&(!companyFilter||j.job.company===companyFilter)&&(!cityFilter||(j.job.locations||[]).some(c=>cityName(c)===cityFilter))&&(filter!=='FAILED'||progress.failed.some(f=>f.id===j.job.id))&&(!onlySelected||selected.has(j.job.id))));}
    const chosen=()=>C.selectedRows(ordered(snapshot.jobs),selected);
    const exportKey=j=>JSON.stringify([j.input_key,j.job,j.excluded_reason]);
    function clearExport(){exportFiles=[];exportIndex=0;exportConsent=false;exportKeys.clear();}
    function selectionChanged(){C.storeSelection(cap.user_id,selected);clearExport();render();}
    function selectionHTML(){
      const jobs=chosen(),canCompare=jobs.length>=2&&new Set(jobs.map(j=>j.job.company)).size===1,canAnalyze=shortlist(jobs,snapshot.settings.round_limit).length;
      const ignoreCount=bulkTargets(jobs,'IGNORED').length,restoreCount=bulkTargets(jobs,'NONE').length;
      return `<div class="selection-bar matching-selection" ${selected.size?'':'hidden'}><div><strong>${selected.size}</strong> 个岗位已选<button id="match-clear-selection" class="btn btn-subtle btn-small" ${preparing||bulkBusy?'disabled':''}>清空</button><button id="match-show-selection" class="btn btn-subtle btn-small">查看已选</button></div><div><button id="match-bulk-ignore" class="btn" title="稍后看和已有投递记录的岗位会保留" ${running||preparing||bulkBusy||!ignoreCount?'disabled':''}>批量忽略（${ignoreCount}）</button>${restoreCount?`<button id="match-bulk-restore" class="btn" ${running||preparing||bulkBusy?'disabled':''}>恢复已忽略（${restoreCount}）</button>`:''}<button id="match-compare-selected" class="btn" ${canCompare?'':'disabled'} title="选择同一公司至少两个岗位">对比已选</button><button id="match-ask-selected" class="btn" ${canCompare&&jobs.length<=8?'':'disabled'} title="选择同一公司 2—8 个岗位，向求职问答询问已有分析">问 Agent</button><button id="match-export" class="btn" ${running||preparing||bulkBusy||!selected.size||selected.size>1000?'disabled':''}>${U.icon('download')}${preparing?'准备分析包…':'导出到 ChatGPT'}</button><button id="match-selected-run" class="btn btn-primary" ${running||preparing||bulkBusy||!canAnalyze?'disabled':''}>${U.icon('spark')}深度分析（${canAnalyze}）</button></div></div>${selected.size>1000?'<p class="pending-note">单次导出最多 1,000 个岗位，请减少选择；API 可分轮处理。</p>':''}`;
    }
    async function bulkPreference(value){
      if(bulkBusy||running||preparing||!current())return;
      const jobs=bulkTargets(chosen(),value);if(!jobs.length)return;
      bulkBusy=true;clearExport();notice=value==='IGNORED'?'正在移出岗位列表…':'正在恢复已忽略岗位…';render();
      let done=0,skipped=0,error='';
      try{
        for(let offset=0;offset<jobs.length;offset+=1000){
          const result=await api('/api/jobs/preferences','PUT',{job_ids:jobs.slice(offset,offset+1000).map(j=>j.job.id),disposition:value});
          const updated=new Set(result.updated);done+=updated.size;skipped+=result.skipped.length;
          for(const row of snapshot.jobs)if(updated.has(row.job.id)){row.disposition=value;if(value==='IGNORED')row.excluded_reason='已忽略，不参与深度分析';selected.delete(row.job.id);}
          C.storeSelection(cap.user_id,selected);if(!current())return;
          notice=`已${value==='IGNORED'?'忽略':'恢复'} ${done} 个岗位，正在处理剩余项…`;render();
        }
      }catch(err){error=err.message;}
      finally{
        if(current()){
          try{await refresh();}catch{error+=(error?' ':'')+'列表刷新失败，请稍后刷新核对。';}
          bulkBusy=false;notice=`已${value==='IGNORED'?'忽略':'恢复'} ${done} 个岗位。${value==='IGNORED'?'已忽略岗位默认隐藏，可在“已忽略”中恢复；稍后看和已有投递记录的岗位会保留。':''}${skipped?` ${skipped} 个岗位因状态变化而保留。`:''}${error?' '+error+' 未确认完成的岗位仍保留在已选中，可核对后重试。':''}`;render();
        }
      }
    }
    function exportHTML(){
      if(!exportFiles.length)return '';
      return `<dialog id="match-export-review" class="modal wide" aria-labelledby="match-export-title">${U.modalHead('match-export-title','核对 ChatGPT 分析包','检查完整文字后，再手动上传到聊天。')}<div class="modal-body"><p>共 ${exportFiles.length} 包、${exportFiles.reduce((n,f)=>n+f.jobCount,0)} 个岗位。下面显示将下载的完整文字，可继续删除敏感内容；修改多包重复的资料时，请保持一致。核对后下载，再手动上传或粘贴到 ChatGPT。不需要 API 密钥，准备和下载不调用模型。ChatGPT 返回 JSON 后，可点击“导入聊天分析”核对并保存。</p><label>查看分析包<select id="match-export-file">${exportFiles.map((f,i)=>`<option value="${i}" ${i===exportIndex?'selected':''}>${esc(f.name)} · ${f.jobCount} 个岗位${f.oversized?' · 长岗位独立包':''}</option>`).join('')}</select></label><label>完整导出文字（可编辑）<textarea id="match-export-text" spellcheck="false">${esc(exportFiles[exportIndex].text)}</textarea></label><label class="check"><input id="match-export-consent" type="checkbox" ${exportConsent?'checked':''}> 我已核对全部分析包，确认可以手动发给 ChatGPT</label><div class="actions"><button id="match-export-download" ${exportConsent?'':'disabled'}>${exportFiles.length===1?'下载分析包（Markdown）':'下载全部分析包（ZIP）'}</button><button id="match-export-copy" ${exportConsent?'':'disabled'}>复制当前包</button><button id="match-export-close">收起分析包</button></div><p class="meta">按公司尽量放在同一包，每包使用相同的分析标准，最多 8 个岗位并按约 48 KB 完整文字分包；长岗位独立成包，原文不会截断。多包 ZIP 内附使用说明和汇总指令。</p></div></dialog>`;
    }
    function importHTML(){
      return `<dialog id="match-import-dialog" class="modal wide" aria-labelledby="match-import-title">${U.modalHead('match-import-title','导入聊天分析','把 ChatGPT 的分析带回岗位雷达。')}<div class="modal-body"><p>上传一个或多个 JSON 结果文件，或粘贴完整 JSON。读取、核对和保存都不调用外部模型。请使用本版导出的分析包。</p><label>选择结果文件（可多选）<input id="match-import-files" type="file" accept=".json,application/json" multiple ${importBusy?'disabled':''}></label><label>或粘贴分析结果<textarea id="match-import-text" spellcheck="false" ${importBusy?'disabled':''} placeholder="粘贴 ChatGPT 返回的 JSON 或 JSON 代码块">${esc(importText)}</textarea></label><details><summary>导出时用了补充遮盖姓名？</summary><label>填写与导出时相同的姓名或称呼<input id="match-import-mask" value="${esc(importMask)}" maxlength="60" autocomplete="off" ${importBusy?'disabled':''}></label><p class="form-note">仅用于本地重新核对脱敏资料，不发送给模型。</p></details>${importError?`<p class="pending-note" role="alert">${esc(importError)}</p>`:''}${importPreview?`<section id="match-import-preview-content"><h3>核对 ${importPreview.jobs.length} 个岗位</h3><p>分数和资格已由本地重新计算。${importPreview.jobs.filter(j=>j.replaces).length} 个岗位已有分析，保存会替换这些结果。原文摘录通过核对仍不代表模型推理必然正确，请阅读逐项结论。</p>${importPreview.evidence_reviews?`<p class="pending-note">${Number(importPreview.evidence_reviews)} 项结论已本地复核，分别标明引用撤销或实践待确认，不影响其他岗位导入。</p>`:''}${importPreview.jobs.map(j=>`<details><summary>${esc(j.company)} · ${esc(j.title)} · ${j.result.score==null?'依据不足':Number(j.result.score).toFixed(1)+' 分'}${j.replaces?' · 替换已有结果':''}</summary><p>核心依据覆盖 ${Number(j.result.coverage).toFixed(1)}% · 投递资格：${esc(D.label('eligibility',j.result.qualifications?.status))}</p>${structureHTML(j.result,{esc})}${renderRequirements(j.result,{esc,D},false)}</details>`).join('')}</section>`:''}</div><div class="modal-foot"><small>本地核对 · 不消耗 API 额度</small><button id="match-import-preview" class="btn" ${importBusy?'disabled':''}>${importBusy?'正在处理…':'核对并预览'}</button><button id="match-import-confirm" class="btn btn-primary" ${importBusy||!importPreview?'disabled':''}>确认导入${importPreview?' '+importPreview.jobs.length+' 个岗位':''}</button></div></dialog>`;
    }
    function invalidateImport(){importRevision++;importPreview=null;importDocument=null;importError='';const content=document.querySelector('#match-import-preview-content');if(content){content.innerHTML='';content.hidden=true;}const b=document.querySelector('#match-import-confirm');if(b)b.disabled=true;}
    function importFailure(error,doc){
      let message=error.message+(error.diagnostic?D.matchingDiagnostic(error.diagnostic):'');
      const index=error.jobIndex??error.diagnostic?.job_index;
      if(!Number.isInteger(index)||index<1||index>(doc?.jobs?.length||0))return message;
      const job=doc.jobs[index-1],row=snapshot.jobs.find(j=>j.job.id===job.job_id),origin=importOrigins.get(job.job_id);
      if(row)message+=` 对应岗位：${row.job.company} · ${row.job.title}。`;
      if(origin)message+=` 所在文件：${origin.name}，文件内第 ${origin.index} 个岗位。`;
      return message;
    }
    async function previewImport(){
      if(importBusy||!current())return;
      invalidateImport();const rev=importRevision;importBusy=true;render();
      let doc;
      try{doc=C.parseDocuments([importText]);const preview=await api('/api/matching/import/preview','POST',{document:doc,mask_name:importMask});if(!current()||panel!=='import'||rev!==importRevision)return;importDocument=doc;importPreview=preview;}
      catch(e){if(current()&&panel==='import'&&rev===importRevision)importError=importFailure(e,doc);}
      finally{if(current()&&panel==='import'&&rev===importRevision){importBusy=false;render();}}
    }
    async function confirmImport(){
      if(importBusy||!importPreview||!importDocument||!current())return;
      const rev=importRevision;importBusy=true;render();let saved=false;
      try{const out=await api('/api/matching/import/confirm','POST',{document:importDocument,mask_name:importMask,preview_key:importPreview.preview_key});saved=true;if(!current())return;if(rev===importRevision){importText='';importPreview=importDocument=null;if(panel==='import')panel='';}notice=`已导入 ${out.imported} 个岗位的聊天分析，可用于岗位对比和准备清单。`;try{await refresh();}catch{notice+=' 列表刷新失败，可稍后手动刷新；结果已保存。';}}
      catch(e){if(current()&&panel==='import'&&rev===importRevision){importError=importFailure(e,importDocument);importPreview=null;}}
      finally{if(current()&&(saved||rev===importRevision)){importBusy=false;render();}}
    }
    const authorized=()=>consentHash===snapshot.candidate_hash;
    function persist(){storeProgress(cap.user_id,progress);}
    function settingsHTML(){return `<dialog id="match-settings-dialog" class="modal" aria-labelledby="match-settings-title">${U.modalHead('match-settings-title','分析设置','预算与处理方式集中在这里维护。')}<form id="match-settings"><div class="modal-body"><div class="form-grid"><label>每轮最多分析岗位数<input name="round_limit" type="number" min="1" max="100" required value="${snapshot.settings.round_limit}"></label><label>每日岗位匹配调用上限<input name="daily_calls" type="number" min="1" max="200" required value="${snapshot.settings.daily_calls}"></label></div><label class="check"><input name="auto_new" type="checkbox" ${snapshot.settings.auto_new?'checked':''}>自动分析新增或变化岗位</label><p class="form-note">自动处理只在本页面开启、本轮资料已核对且已经确认分析后运行。缓存命中不发请求，失败或超时的尝试也计入上限。每天按北京时间重置。</p><details><summary>查看调用与暂停规则</summary><p class="form-note">顺序处理，每批最多 3 个岗位并限制文字量。暂停后当前批次完成，再停止后续请求。关闭页面后本轮继续处理；服务中断后需要重新核对再继续。</p></details>${notice?`<p class="modal-notice" role="status">${esc(notice)}</p>`:''}</div><div class="modal-foot"><small>模型 API 独立计费</small><button class="btn btn-primary" ${running||preparing?'disabled':''}>保存设置</button></div></form></dialog>`;}
    function reviewHTML(){
      const jobs=(intent?.ids||[]).map(id=>snapshot.jobs.find(j=>j.job.id===id)).filter(Boolean);
      return `<dialog id="match-review-dialog" class="modal wide" aria-labelledby="match-review-title">${U.modalHead('match-review-title','核对本轮外发资料','检查岗位与资料，再确认发起分析。')}<div class="modal-body"><h3>本轮 ${jobs.length} 个岗位</h3><div class="review-jobs">${jobs.map(j=>`<div class="review-job"><span>${esc(D.text(j.job.title))}</span><small>${esc(D.text(j.job.company))}</small></div>`).join('')}</div><p class="form-note">当前模型：${esc(root.CampusModels.label(cap))} <button type="button" id="match-model" class="text-btn">选择模型</button></p><p class="form-note">简历文件、账号邮箱、联系方式、项目链接和密钥不进入匹配文字。仍请核对项目文字里的姓名或称呼。</p><form id="match-mask"><label>补充遮盖姓名或称呼（可选）<input name="mask_name" value="${esc(maskName)}" maxlength="60" autocomplete="off" placeholder="仅用于本轮本机脱敏"></label><button class="btn btn-small" ${reviewBusy?'disabled':''}>更新脱敏预览</button></form><details class="disclosure" open><summary>候选人资料与项目事实（${snapshot.candidate.facts.length} 条）</summary><div><ul class="match-review-facts">${snapshot.candidate.facts.map(f=>`<li><b>${f.project_name?esc(f.project_name)+' · ':''}${esc(labels[f.kind]||f.kind)}</b>：${esc(f.kind==='DEGREE'?D.label('degree',f.text):D.text(f.text))}</li>`).join('')}</ul></div></details><details class="disclosure"><summary>所选岗位的完整外发文字</summary><div>${reviewBusy?'<p>正在从本地记录准备脱敏文字…</p>':reviewError?`<p class="pending-note">${esc(reviewError)}</p>`:(reviewBundle?.jobs||[]).map(j=>`<h3>${esc(j.title)}</h3><div class="drawer-original">${esc(j.text)}</div>`).join('<hr>')}</div></details>${reviewError?`<p class="pending-note">${esc(reviewError)}</p><button id="match-review-reload" class="btn btn-small">重新准备预览</button>`:''}<div class="note-box">点击「确认并开始分析」后才会调用模型。最多按当前每轮上限处理，成功结果逐批保存，失败项可单独重试。</div><label class="check-label"><input id="match-consent" type="checkbox" ${authorized()?'checked':''} ${reviewBusy||!reviewBundle?'disabled':''}>我已核对以上全部资料与岗位文字，同意将本轮脱敏资料发送给所选模型。</label></div><div class="modal-foot"><small>今日调用 ${snapshot.calls_today} / ${snapshot.settings.daily_calls}</small><div><button type="button" class="btn" data-dialog-close>取消</button><button id="match-confirm" class="btn btn-primary" ${!authorized()||!reviewBundle||reviewBusy||!root.CampusModels.available(cap)||snapshot.calls_today>=snapshot.settings.daily_calls?'disabled':''}>确认并开始分析</button></div></div></dialog>`;
    }
    async function requestAnalysis(ids,retry=false,continuing=false){
      if(running||preparing||bulkBusy||!current())return;
      const available=new Set(snapshot.jobs.filter(eligible).map(j=>j.job.id));
      ids=[...new Set(ids)].filter(id=>available.has(id));
      if(!ids.length){notice='没有待分析且可分析的岗位。';render();return;}
      intent={ids,retry,continuing};panel='review';consentHash='';paused=true;reviewBusy=true;reviewBundle=null;reviewError='';
      const revision=++reviewRevision,hash=snapshot.candidate_hash;
      reviewKeys=new Map(ids.map(id=>{const j=snapshot.jobs.find(j=>j.job.id===id);return [id,j.input_key];}));
      render();
      try{const bundle=await api('/api/matching/export','POST',{...identity(),job_ids:ids,candidate_hash:hash});if(!current()||revision!==reviewRevision)return;if(bundle.candidate_hash!==snapshot.candidate_hash)throw new UserError('求职资料已变化，请重新核对外发资料。');reviewBundle=bundle;}
      catch(err){if(revision===reviewRevision)reviewError=err.message;}
      finally{if(current()&&revision===reviewRevision){reviewBusy=false;render();}}
    }
    async function confirmAnalysis(){
      if(!authorized()||!reviewBundle||!intent||running||reviewBusy)return;
      const work=intent;
      try{await refresh();if(!current())return;if(!authorized()||reviewBundle.candidate_hash!==snapshot.candidate_hash||[...reviewKeys].some(([id,key])=>snapshot.jobs.find(j=>j.job.id===id)?.input_key!==key)){notice='资料或岗位已有变化，请核对更新后的文字。';await requestAnalysis(work.ids,work.retry,work.continuing);return;}}
      catch(err){notice=err.message;render();return;}
      panel='';await start(work.ids,work.retry,work.continuing);
    }
    function progressHTML(actions){
      if(durable&&task)return Tasks.render(task,taskRuns,{esc,D},notice);
      const hasQueue=running||progress.pending.length||progress.failed.length;
      if(!hasQueue&&!notice)return '';
      if(!hasQueue)return `<section class="match-progress match-progress-compact" aria-label="本轮分析任务"><p role="status">${esc(notice)}</p><button id="match-dismiss-notice" class="btn btn-subtle btn-small">知道了</button></section>`;
      return `<section class="match-progress" aria-label="本轮分析任务"><p role="status">${esc(notice||`本轮已完成 ${progress.done} 个，待处理 ${progress.pending.length} 个，失败 ${progress.failed.length} 个。`)}</p><div class="actions"><button id="match-continue" class="btn btn-small" ${actions.continue.disabled?'disabled':''} title="${esc(actions.continue.reason)}">继续未完成的本轮（${progress.pending.length}）</button><button id="match-retry" class="btn btn-small" ${actions.retry.disabled?'disabled':''} title="${esc(actions.retry.reason)}">重试失败项（${progress.failed.length}）</button><button id="match-pause" class="btn btn-small" ${running?'':'disabled'}>暂停后续分析</button></div><p id="match-actions-help" class="meta">${esc(actions.reason||[actions.continue.reason,actions.retry.reason].filter(Boolean).join(' '))} 点击继续或重试后，先核对本轮外发资料；重试逐个分析并保留其他未完成项。</p>${progress.failed.length?`<details><summary>查看失败项与原因</summary><ul>${progress.failed.map(f=>`<li>${esc(snapshot.jobs.find(j=>j.job.id===f.id)?.job.title||'岗位')}：${esc(f.message)}</li>`).join('')}</ul></details>`:''}</section>`;
    }
    function render(){
      if(!current())return;
      if(companyFilter&&!snapshot.jobs.some(j=>j.job.company===companyFilter))companyFilter='';
      if(cityFilter&&!snapshot.jobs.some(j=>(j.job.locations||[]).some(c=>cityName(c)===cityFilter)))cityFilter='';
      if(lastJob&&!snapshot.jobs.some(j=>j.job.id===lastJob)){lastJob='';drawerOpen=false;}
      const saved=U.capture(),view=rows(),totalPages=Math.max(1,Math.ceil(view.length/50));pageNo=Math.min(pageNo,totalPages);
      const viewKey=JSON.stringify([query,filter,tierFilter,workflowFilter,companyFilter,cityFilter,directionFilter,showIgnored,sort,pageNo,onlySelected]);
      if(viewKey!==renderedViewKey)saved.regions=(saved.regions||[]).filter(region=>region.id!=='match-job-list');renderedViewKey=viewKey;
      saveBrowse();
      const slice=view.slice((pageNo-1)*50,pageNo*50),availableIDs=new Set(snapshot.jobs.filter(eligible).map(j=>j.job.id));
      const actions=analysisActions(progress,{running,preparing,authorized:true,modelAvailable:root.CampusModels.available(cap),callsToday:snapshot.calls_today,dailyCalls:snapshot.settings.daily_calls,runCount:shortlist(view,snapshot.settings.round_limit).length,pendingCount:progress.pending.filter(id=>availableIDs.has(id)).length,failedCount:progress.failed.filter(f=>availableIDs.has(f.id)).length});
      const companies=[...new Set(snapshot.jobs.map(j=>j.job.company))],cities=[...new Set(snapshot.jobs.flatMap(j=>(j.job.locations||[]).map(cityName)))].sort();
      const recent=snapshot.jobs.find(j=>j.job.id===lastJob);
      const directionRows=rows('ALL'),unrelated=bulkTargets(view.filter(j=>directionOf(j)==='UNRELATED'),'IGNORED');
      const directionViews={MAIN:'主投及相关',ALL:'全部方向',...directions};
      const hiddenIgnored=snapshot.jobs.filter(j=>j.disposition==='IGNORED').length;
      const visibleCatalog=snapshot.jobs.filter(j=>catalogVisible(j,workflowFilter,showIgnored));
      const html=U.heading('岗位雷达','先看相关机会，再决定把时间花在哪里。',`<button id="match-open-import" class="btn" ${running||preparing||bulkBusy?'disabled':''}>${U.icon('upload')}导入聊天分析</button><button id="match-open-comparison" class="btn" aria-label="同公司对比" ${snapshot.jobs.length?'':'disabled'}>${U.icon('list')}同公司对比</button><button id="match-source" class="btn" aria-label="关注来源">${U.icon('radar')}关注来源</button><button id="match-open-settings" class="btn" aria-label="分析设置">${U.icon('filter')}分析设置</button>`)+journeyHTML(snapshot.jobs,{esc},!!targetRoles().length,workflowFilter)+progressHTML(actions)+`
        <div class="matching-workbench"><section class="workspace-panel workbench-inventory" aria-label="岗位筛选与列表"><div class="company-toolbar"><div class="company-switch">${U.icon('briefcase')}<label><span class="visually-hidden">按公司筛选</span><select id="match-company" aria-label="按公司筛选"><option value="">全部公司（${visibleCatalog.length}）</option>${companies.map(c=>`<option value="${esc(c)}" ${companyFilter===c?'selected':''}>${esc(c)}（${visibleCatalog.filter(j=>j.job.company===c).length}）</option>`).join('')}</select></label></div><button id="match-compare-company" class="text-btn" ${snapshot.jobs.length?'':'disabled'}>同公司对比</button></div>
        <div class="direction-filter"><div class="direction-context"><span>主投方向：${esc(targetRoles().join('、')||'尚未设置')}</span><button id="match-edit-direction" class="text-btn">修改求职资料</button></div><div class="direction-tabs" aria-label="方向相关性筛选">${Object.entries(directionViews).filter(([value])=>['MAIN','ALL','UNCERTAIN'].includes(value)).map(([value,label])=>`<button id="match-direction-${value}" class="direction-tab" aria-pressed="${directionFilter===value}">${label}<span class="tab-count">${directionRows.filter(j=>directionMatch(j,value)).length}</span></button>`).join('')}<details class="direction-more" data-remember id="match-direction-more" ${['MATCH','RELATED','UNRELATED'].includes(directionFilter)?'open':''}><summary>细分方向</summary><div>${Object.entries(directionViews).filter(([value])=>!['MAIN','ALL','UNCERTAIN'].includes(value)).map(([value,label])=>`<button id="match-direction-${value}" class="direction-tab" aria-pressed="${directionFilter===value}">${label}<span class="tab-count">${directionRows.filter(j=>directionMatch(j,value)).length}</span></button>`).join('')}</div></details></div><details class="workbench-method"><summary>方向筛选说明${hiddenIgnored?' · 已忽略 '+hiddenIgnored+' 个':''}</summary><p class="form-note">按标题与职责在本地判断；待判断岗位保留供核对，方向相关不代表技能匹配。${hiddenIgnored&&!showIgnored&&workflowFilter!=='IGNORED'?`已隐藏 ${hiddenIgnored} 个已忽略岗位。`:''}</p></details></div>
        <form id="match-filter" class="matching-filter"><div class="form-grid"><label class="search-field">${U.icon('search')}<input id="match-search" name="q" type="search" aria-label="搜索公司或岗位" placeholder="搜索公司或岗位" value="${esc(query)}"></label><details class="workbench-filters" data-remember id="match-more-filters" ${cityFilter||filter||workflowFilter||tierFilter||sort==='deep'?'open':''}><summary>${U.icon('filter')}更多筛选${cityFilter||filter||workflowFilter||tierFilter||sort==='deep'?' · 已启用':''}</summary><div><label><select id="match-city" name="city" aria-label="城市筛选"><option value="">所有城市</option>${cities.map(c=>`<option ${cityFilter===c?'selected':''}>${esc(c)}</option>`).join('')}</select></label><label><select id="match-state" name="state" aria-label="分析状态筛选"><option value="">所有状态</option>${Object.entries({BASIC:'待分析',ANALYZED:'已分析',STALE:'待更新',FAILED:'失败项'}).map(([s,n])=>`<option value="${s}" ${filter===s?'selected':''}>${n}</option>`).join('')}</select></label><label><select id="match-workflow" name="workflow" aria-label="操作状态筛选"><option value="">全部操作状态（默认隐藏已忽略）</option>${Object.entries(workflows).map(([value,label])=>`<option value="${value}" ${workflowFilter===value?'selected':''}>${label}</option>`).join('')}</select></label><label><select id="match-tier" name="tier" aria-label="初筛建议"><option value="">所有初筛建议</option>${Object.entries(tiers).map(([key,n])=>`<option value="${key}" ${tierFilter===key?'selected':''}>${n}</option>`).join('')}</select></label><label><select id="match-sort" name="sort" aria-label="岗位排序"><option value="local" ${sort==='local'?'selected':''}>初筛优先</option><option value="deep" ${sort==='deep'?'selected':''}>匹配度优先</option></select></label></div></details></div><div class="list-meta" style="padding:14px 0 0"><div><span>${view.length} 个岗位</span><label class="quiet-check"><input id="match-show-ignored" name="show_ignored" type="checkbox" ${showIgnored?'checked':''}>显示已忽略</label><label class="quiet-check"><input id="match-only-selected" name="only_selected" type="checkbox" ${onlySelected?'checked':''}>只看已选</label></div><button type="button" id="match-select-top" class="btn btn-subtle btn-small" ${preparing?'disabled':''}>${U.icon('plus')}选择优先分析岗位</button></div></form>
        ${recent?`<div class="browse-context"><span>上次查看：<button id="match-recent" class="text-btn">${esc(recent.job.title)}</button></span><button id="match-clear-filters" class="btn btn-subtle btn-small">清除筛选</button></div>`:query||filter||tierFilter||workflowFilter||companyFilter||cityFilter||onlySelected||directionFilter!=='ALL'||showIgnored?'<div class="browse-context"><span>已保留当前筛选</span><button id="match-clear-filters" class="btn btn-subtle btn-small">清除筛选</button></div>':''}
        <details class="workbench-selection-tools" data-remember id="match-selection-tools"><summary>批量选择与分析</summary><div class="selection-options">${unrelated.length?`<button id="match-select-unrelated" class="btn btn-subtle btn-small" title="替换已选，跨页选择当前筛选范围中未收藏且没有投递记录的明显无关岗位" ${preparing||bulkBusy?'disabled':''}>选择明显无关（${unrelated.length}）</button>`:''}<button id="match-select-page" class="btn btn-subtle btn-small" ${preparing||bulkBusy||!slice.length?'disabled':''}>选择本页（${slice.length}）</button><button id="match-select-all" class="btn btn-subtle btn-small" ${preparing||bulkBusy||!view.length?'disabled':''}>选择全部结果（${view.length}）</button><button id="match-run" class="btn btn-subtle btn-small" ${actions.run.disabled?'disabled':''} title="${esc(actions.run.reason)}">分析前 ${snapshot.settings.round_limit} 个待分析岗位</button></div></details>
        <div id="match-job-list" data-scroll-region class="workbench-job-list" aria-label="当前筛选岗位">${slice.map(j=>jobRowHTML(j,{esc,D},selected.has(j.job.id),drawerOpen&&lastJob===j.job.id,preparing||bulkBusy,progress.failed.some(f=>f.id===j.job.id))).join('')||'<div class="empty-state"><h3>没有找到符合条件的岗位</h3><p>试试其他关键词，或清除筛选条件。</p><button id="match-reset" class="btn">清除筛选</button></div>'}</div>
        <div class="table-footer"><span>${U.icon('shield')}本地初筛无需模型 · 今日调用 ${snapshot.calls_today} / ${snapshot.settings.daily_calls}</span><span><button id="match-refresh" class="btn btn-small" ${running||preparing||bulkBusy?'disabled':''}>刷新</button> 第 ${pageNo} / ${totalPages} 页 <button id="match-prev" class="btn btn-small" ${pageNo===1?'disabled':''}>上一页</button><button id="match-next" class="btn btn-small" ${pageNo===totalPages?'disabled':''}>下一页</button></span></div></section><aside id="match-detail" data-scroll-region class="workbench-detail" aria-label="岗位详情"><div class="workbench-detail-empty"><span>${U.icon('radar')}</span><h2>把一份机会看清楚</h2><p>选择左侧岗位，在这里核对匹配依据、岗位原文和下一步行动。</p><small>读取已保存结果，不会自动调用模型。</small></div></aside></div>${selectionHTML()}<p class="meta" style="margin:18px 0 30px">当前模型：${esc(root.CampusModels.label(cap))}。已忽略岗位默认隐藏，可切换“已忽略”批量恢复。已关闭或明确不符合的岗位保留供核对，分析时跳过。</p>${settingsHTML()}${reviewHTML()}${exportHTML()}${importHTML()}${comparisonHTML()}`;
      U.releaseDrawer();
      if(!set(html))return;
      U.placeDrawer(document.querySelector('#match-detail'));
      const $=s=>document.querySelector(s),on=(id,fn)=>{const el=$('#'+id);if(el)el.onclick=fn;};
      const companySelect=$('#match-company');if(companySelect)companySelect.onchange=e=>{companyFilter=e.target.value;pageNo=1;render();};
      on('match-compare-company',()=>openComparison('FILTERED'));
      for(const b of document.querySelectorAll('[data-match-journey]'))b.onclick=()=>{workflowFilter={saved:'SAVED',planned:'PLANNED',applied:'APPLIED'}[b.dataset.matchJourney]||'';query=filter=tierFilter=companyFilter=cityFilter='';directionFilter=b.dataset.matchJourney==='discover'&&targetRoles().length?'MAIN':'ALL';onlySelected=showIgnored=false;pageNo=1;render();};
      for(const value of Object.keys(directionViews))on('match-direction-'+value,()=>{directionFilter=value;onlySelected=false;pageNo=1;render();});
      on('match-edit-direction',()=>navigate('profile'));
      on('match-select-unrelated',()=>{if(preparing||bulkBusy)return;selected.clear();C.addSelection(selected,unrelated);selectionChanged();});
      on('match-bulk-ignore',()=>bulkPreference('IGNORED'));on('match-bulk-restore',()=>bulkPreference('NONE'));
      on('match-open-comparison',()=>openComparison());on('match-compare-selected',()=>openComparison('SELECTED'));
      on('match-ask-selected',()=>{const jobs=chosen();if(jobs.length<2||jobs.length>8||new Set(jobs.map(j=>j.job.company)).size!==1)return;navigate('agent',{message:`请对比 ${jobs[0].job.company} 的这些岗位，说明当前深度匹配依据、待核对项和是否存在并列：${jobs.map(j=>j.job.id).join('、')}`});});
      if(panel==='company'){
        $('#match-comparison-company').onchange=e=>{comparisonCompany=e.target.value;comparisonReport=null;comparisonError='';comparisonRevision++;comparisonBusy=false;render();};
        $('#match-comparison-scope').onchange=e=>{comparisonScope=e.target.value;comparisonReport=null;comparisonError='';comparisonRevision++;comparisonBusy=false;render();};
        $('#match-comparison-form').onsubmit=e=>{e.preventDefault();return loadComparison();};
        for(const b of document.querySelectorAll('[data-compare-detail]'))b.onclick=()=>{document.querySelector('#match-comparison-dialog').close();openMatchDetail(b.dataset.compareDetail);};
        for(const b of document.querySelectorAll('[data-compare-prepare]'))b.onclick=()=>{document.querySelector('#match-comparison-dialog').close();openMatchDetail(b.dataset.comparePrepare,'preparation');};
      }
      on('match-dismiss-notice',()=>{notice='';render();});on('match-source',()=>navigate('watches'));on('match-open-settings',()=>{panel='settings';render();});
      on('match-open-import',()=>{panel='import';importError='';render();});
      on('match-import-preview',previewImport);on('match-import-confirm',confirmImport);
      $('#match-import-text').oninput=e=>{importText=e.target.value;importOrigins.clear();invalidateImport();};$('#match-import-mask').oninput=e=>{importMask=e.target.value.trim();invalidateImport();};
      $('#match-import-files').onchange=async e=>{
        const files=Array.from(e.target.files||[]);if(!files.length)return;importOrigins.clear();invalidateImport();const rev=importRevision;importBusy=true;render();
        try{if(files.reduce((n,f)=>n+f.size,0)>2*1024*1024)throw new Error('结果文件超过 2 MB，请分批导入。');const texts=await Promise.all(files.map(f=>f.text())),origins=new Map();const doc=C.parseDocuments(texts,(id,fileIndex,index)=>origins.set(id,{name:String(files[fileIndex].name||'所选文件'),index}));if(current()&&panel==='import'&&rev===importRevision){importText=JSON.stringify(doc,null,2);importOrigins=origins;}}
        catch(err){if(current()&&panel==='import'&&rev===importRevision)importError=err.message;}finally{if(current()&&panel==='import'&&rev===importRevision){importBusy=false;render();}}
      };
      on('match-model',()=>{paused=true;navigate('models');});
      $('#match-consent').onchange=e=>{consentHash=e.target.checked?snapshot.candidate_hash:'';paused=true;render();};
      on('match-confirm',confirmAnalysis);on('match-review-reload',()=>requestAnalysis(intent.ids,intent.retry,intent.continuing));
      $('#match-settings').onsubmit=async e=>{e.preventDefault();if(!e.target.reportValidity())return;const f=new FormData(e.target);try{snapshot.settings=await api('/api/matching/settings','PUT',{round_limit:Number(f.get('round_limit')),daily_calls:Number(f.get('daily_calls')),auto_new:f.get('auto_new')==='on'});notice='分析设置已保存。';panel='';render();}catch(err){notice=err.message;render();}};
      $('#match-mask').onsubmit=async e=>{e.preventDefault();const name=String(new FormData(e.target).get('mask_name')||'').trim();if(name&&name.length<2){consentHash='';reviewError='请填写至少两个字的姓名或称呼。';render();return;}clearExport();maskName=name;consentHash='';const work=intent;try{await refresh();await requestAnalysis(work.ids,work.retry,work.continuing);}catch(err){reviewError=err.message;render();}};
      const applyFilter=()=>{clearTimeout(searchTimer);const f=new FormData($('#match-filter'));query=String(f.get('q')||'');filter=String(f.get('state')||'');tierFilter=String(f.get('tier')||'');const nextWorkflow=String(f.get('workflow')||'');const openingIgnored=nextWorkflow==='IGNORED'&&workflowFilter!=='IGNORED';if(openingIgnored)directionFilter='ALL';workflowFilter=nextWorkflow;showIgnored=f.get('show_ignored')==='on';cityFilter=String(f.get('city')||'');sort=f.get('sort');onlySelected=!openingIgnored&&f.get('only_selected')==='on';pageNo=1;render();};
      $('#match-filter').onsubmit=e=>{e.preventDefault();clearTimeout(searchTimer);applyFilter();};
      $('#match-filter').onchange=applyFilter;
      $('#match-search').oninput=()=>{clearTimeout(searchTimer);searchTimer=setTimeout(()=>{if(current())applyFilter();},240);};
      const resetFilters=()=>{query='';filter='';tierFilter='';workflowFilter='';cityFilter='';companyFilter='';onlySelected=false;directionFilter='ALL';showIgnored=false;pageNo=1;render();};
      on('match-reset',resetFilters);on('match-clear-filters',resetFilters);on('match-recent',()=>openMatchDetail(lastJob,lastTab));
      for(const b of document.querySelectorAll('[data-match-company]'))b.onclick=()=>{companyFilter=b.dataset.matchCompany;pageNo=1;render();};
      on('match-run',()=>requestAnalysis(shortlist(rows(),snapshot.settings.round_limit).map(j=>j.job.id)));
      on('match-select-page',()=>{C.addSelection(selected,slice);selectionChanged();});on('match-select-all',()=>{C.addSelection(selected,view);selectionChanged();});on('match-select-top',()=>{C.addSelection(selected,shortlist(view,snapshot.settings.round_limit));selectionChanged();});
      on('match-clear-selection',()=>{selected.clear();selectionChanged();});on('match-show-selection',()=>{directionFilter='ALL';showIgnored=true;onlySelected=true;query='';filter='';tierFilter='';workflowFilter='';cityFilter='';companyFilter='';pageNo=1;render();});
      on('match-selected-run',()=>requestAnalysis(shortlist(chosen(),snapshot.settings.round_limit).map(j=>j.job.id)));on('match-export',prepareExport);
      for(const input of document.querySelectorAll('[data-match-select]'))input.onchange=()=>{if(input.checked)selected.add(input.dataset.matchSelect);else selected.delete(input.dataset.matchSelect);selectionChanged();};
      if(exportFiles.length){
        $('#match-export-file').onchange=e=>{exportIndex=Number(e.target.value);render();};
        $('#match-export-text').oninput=e=>{exportFiles[exportIndex].text=e.target.value;exportConsent=false;$('#match-export-consent').checked=false;$('#match-export-download').disabled=true;$('#match-export-copy').disabled=true;};
        $('#match-export-consent').onchange=e=>{exportConsent=e.target.checked;$('#match-export-download').disabled=!exportConsent;$('#match-export-copy').disabled=!exportConsent;};
        on('match-export-close',()=>{clearExport();panel='';render();});
        on('match-export-download',async()=>{if(!exportConsent)return;try{await C.download(exportFiles);notice='分析包已下载，请手动上传到 ChatGPT。';}catch(err){notice='无法下载分析包，请重试或复制当前包。';}render();});
        on('match-export-copy',async()=>{if(!exportConsent)return;try{await root.navigator.clipboard.writeText(exportFiles[exportIndex].text);notice='当前分析包已复制，可以粘贴到 ChatGPT。';}catch(err){notice='此浏览器暂不能自动复制，请从预览框手动复制完整文字。';}render();});
      }
      on('match-continue',()=>requestAnalysis([...progress.pending],false,true));on('match-retry',()=>requestAnalysis(progress.failed.map(f=>f.id).filter(id=>availableIDs.has(id)).slice(0,snapshot.settings.round_limit),true));
      on('match-refresh',async()=>{try{await refresh();notice='岗位与分析进度已刷新。';render();}catch(err){notice=err.message;render();}});
      on('match-pause',async()=>{if(durable){await taskControl('PAUSE');return;}paused=true;notice='已暂停后续分析，当前批次完成后停止。';render();});
      on('match-task-cancel',()=>taskControl('CANCEL'));
      on('match-task-events',async()=>{const id=task?.id;try{const events=await api('/api/matching/tasks/'+encodeURIComponent(id)+'/events');if(current()&&task?.id===id)document.querySelector('#match-task-events-box').innerHTML=Tasks.traceHTML(events,{esc,D});}catch(e){notice=e.message;render();}});
      for(const b of document.querySelectorAll('[data-match-task]'))b.onclick=()=>{applyTask(taskRuns.find(v=>v.id===b.dataset.matchTask));notice='';render();};
      on('match-prev',()=>{pageNo--;render();});on('match-next',()=>{pageNo++;render();});
      for(const b of document.querySelectorAll('[data-match-job]'))b.onclick=()=>openMatchDetail(b.dataset.matchJob);
      const dialogID={settings:'match-settings-dialog',review:'match-review-dialog',export:'match-export-review',import:'match-import-dialog',company:'match-comparison-dialog'}[panel];
      if(dialogID){const dialog=$('#'+dialogID);U.openDialog(dialog,()=>{if(document.querySelector('#'+dialogID)!==dialog)return;panel='';if(dialogID==='match-import-dialog'){importRevision++;importBusy=false;importPreview=null;}if(dialogID==='match-comparison-dialog'){comparisonRevision++;comparisonBusy=false;}if(dialogID==='match-review-dialog'){consentHash='';paused=true;reviewRevision++;reviewBusy=false;}});}
      U.restore(saved);
    }
    async function openMatchDetail(id,initialTab='overview',focus=true){
      let row=snapshot.jobs.find(j=>j.job.id===id);if(!row)return;
      if(id!==lastJob){const host=document.querySelector('#match-detail');if(host)host.scrollTop=0;}
      lastJob=id;lastTab=['overview','evidence','source','preparation'].includes(initialTab)?initialTab:'overview';drawerOpen=true;saveBrowse();
      let application=null,applicationReady=false,loading=true,officialURL='',loadPreparation=null;
      const header=()=>`<div class="drawer-top"><div class="drawer-topline"><span>${esc(D.text(row.job.company))}</span><span class="workbench-detail-state">${esc(row.source==='CHATGPT_IMPORT'?'聊天导入 · '+(labels[row.state]||'待核对'):labels[row.state]||'待核对')}</span><button type="button" class="icon-btn" data-dialog-close aria-label="关闭岗位详情">${U.icon('close')}</button></div><h2 id="drawer-title" tabindex="-1">${esc(D.text(row.job.title))}</h2><p class="drawer-subtitle">${esc((row.job.locations||[]).map(D.text).join(' / '))} · ${esc(D.label('job_type',row.job.job_type))}</p>${drawerActions(row,{application,applicationReady,loading,officialURL},{esc,D,U})}</div>`;
      const opened=U.drawer(header()+'<div class="drawer-loading">正在读取本地保存的结果与岗位原文…</div>');lastOpened=opened;
      for(const item of document.querySelectorAll('[data-workbench-row]'))item.classList.toggle('is-current',item.dataset.workbenchRow===id);
      for(const button of document.querySelectorAll('[data-match-job]')){if(button.dataset.matchJob===id)button.setAttribute('aria-current','true');else button.removeAttribute('aria-current');}
      if(focus){opened.element.querySelector('#drawer-title').focus({preventScroll:true});if(root.matchMedia?.('(max-width: 760px)').matches)opened.element.scrollIntoView({block:'start',behavior:'instant'});}
      const valid=()=>current()&&U.drawerCurrent(opened.revision);
      opened.element.addEventListener('close',()=>{if(current()&&lastOpened===opened&&!opened.element.open){drawerOpen=false;saveBrowse();}},{once:true});
      const updateHeader=focus=>{if(!valid())return;opened.element.querySelector('.drawer-top').outerHTML=header();bindHeader();if(focus)opened.element.querySelector(focus)?.focus({preventScroll:true});};
      const bindHeader=()=>{
        const plan=opened.element.querySelector('[data-overview-plan]');if(plan){plan.textContent=application?'查看投递进展':loading?'读取投递状态…':applicationReady?'加入投递计划':'重试读取投递状态';plan.disabled=loading;plan.onclick=()=>opened.element.querySelector('[data-detail-plan]').click();}
        const preparation=opened.element.querySelector('[data-overview-preparation]');if(preparation)preparation.onclick=()=>opened.element.querySelector('[data-detail-preparation]').click();
        const evidence=opened.element.querySelector('[data-overview-evidence]');if(evidence)evidence.onclick=()=>{lastTab='evidence';saveBrowse();U.activateTab(opened.element.querySelector('#drawer-tab-evidence'));};
        const analyze=opened.element.querySelector('[data-overview-analyze]');if(analyze)analyze.onclick=()=>opened.element.querySelector('[data-detail-analyze]').click();
        opened.element.querySelector('[data-detail-preparation]').onclick=()=>{lastTab='preparation';saveBrowse();const tab=opened.element.querySelector('#drawer-tab-preparation');if(tab){U.activateTab(tab);loadPreparation?.();}else initialTab='preparation';};
        opened.element.querySelector('[data-detail-analyze]').onclick=()=>{opened.element.close();requestAnalysis([id]);};
        opened.element.querySelector('[data-detail-plan]').onclick=async e=>{
          if(application){navigate('applications',{applicationID:application.id});return;}
          e.currentTarget.disabled=true;
          try{
            const apps=await api('/api/applications');if(!valid())return;
            application=apps.find(a=>a.job_id===id)||row.application||null;applicationReady=true;
            if(!application)application=await api('/api/applications','POST',{job_id:id});
            if(!valid())return;row.application=application;updateHeader('[data-detail-plan]');render();U.notify(application.current_state==='PLANNED'?'已加入投递计划，可查看投递进展。':'已有投递记录，可查看投递进展。');
          }catch(err){if(valid()){updateHeader('[data-detail-plan]');U.notify(err.message);}}
        };
        for(const button of opened.element.querySelectorAll('[data-detail-pref]'))button.onclick=async()=>{
          const disposition=row.disposition===button.dataset.detailPref?'NONE':button.dataset.detailPref;
          for(const b of opened.element.querySelectorAll('[data-detail-pref]'))b.disabled=true;
          try{
            await api('/api/jobs/'+encodeURIComponent(id)+'/preference','PUT',{disposition});if(!valid())return;
            row.disposition=disposition;
            if(disposition==='IGNORED')row.excluded_reason='已忽略，不参与深度分析';
            let message=disposition==='SAVED'?'已加入稍后看。':disposition==='IGNORED'?'已忽略；岗位仍保留，深度分析会跳过。':'已恢复默认。';
            try{await refresh();}catch{message+=' 岗位列表刷新失败，请稍后刷新。';}
            if(!valid())return;row=snapshot.jobs.find(j=>j.job.id===id)||row;render();updateHeader('[data-detail-pref="'+button.dataset.detailPref+'"]');U.notify(message);
          }catch(err){if(valid()){updateHeader('[data-detail-pref="'+button.dataset.detailPref+'"]');U.notify(err.message);}}
        };
      };
      bindHeader();
      try{
        const [result,record,apps]=await Promise.all([api('/api/matching/results/'+encodeURIComponent(id),'POST',identity()),api('/api/jobs/'+encodeURIComponent(id)),api('/api/applications').catch(()=>null)]);
        if(!current()||!U.drawerCurrent(opened.revision))return;
        loading=false;applicationReady=Array.isArray(apps);application=apps?.find(a=>a.job_id===id)||row.application||null;officialURL=root.CampusApplications.officialLink(record.official_url);row={...row,state:result.state,excluded_reason:result.excluded_reason,local:result.local||row.local};
        const observations=record.observations||[],latest=observations.find(o=>o.text)||null;
        const tabs=[['overview','匹配与行动'],['evidence','逐项依据'],['source','岗位原文'],['preparation','准备清单']];
        opened.element.innerHTML=header()+`<div class="drawer-body"><div class="detail-tabs" role="tablist" aria-label="岗位详情视图">${tabs.map(([key,n],i)=>`<button id="drawer-tab-${key}" role="tab" aria-selected="${i===0}" aria-controls="drawer-${key}" tabindex="${i===0?0:-1}" data-view-tab>${n}</button>`).join('')}</div><div id="drawer-overview" role="tabpanel" aria-labelledby="drawer-tab-overview">${overviewHTML(result,row,{esc,D,U})}<button class="btn btn-small" data-detail-record>查看完整岗位核验记录</button></div><div id="drawer-evidence" role="tabpanel" aria-labelledby="drawer-tab-evidence" hidden>${result.result?`${result.state==='STALE'?'<p class="pending-note">以下是过期结果，仅供核对。需要重新分析后才能用于当前判断。</p>':''}${renderRequirements(result.result,{esc},result.state==='ANALYZED')}`:'<p class="empty">尚未产生模型逐项分析，可先查看本地初筛依据。</p>'+renderLocal(row,{esc})}</div><div id="drawer-source" role="tabpanel" aria-labelledby="drawer-tab-source" hidden><p class="meta">最近可用岗位观察：${latest?esc(D.date(latest.observed_at)):'暂无记录'}。原文保留来源语言，不代表仍可投递。</p><div class="drawer-original">${esc(latest?.text||'目前没有可用的岗位原文。')}</div><button class="btn btn-small" data-detail-record>查看完整岗位核验记录</button></div><div id="drawer-preparation" role="tabpanel" aria-labelledby="drawer-tab-preparation" hidden></div></div>`;
        let preparationLoaded=false;
        const supplement=requirementID=>navigate('profile',{evidence:{jobID:id,requirementID,identity:identity()}});
        for(const b of opened.element.querySelectorAll('[data-supplement]'))b.onclick=()=>supplement(b.dataset.supplement);
        loadPreparation=async()=>{
          if(preparationLoaded)return;preparationLoaded=true;
          const box=opened.element.querySelector('#drawer-preparation');box.innerHTML='<p class="empty">正在读取已保存的分析，不调用模型…</p>';
          try{
            const plan=await api('/api/matching/preparation/'+encodeURIComponent(id),'POST',identity());
            if(!current()||!U.drawerCurrent(opened.revision))return;
            const done=Decision.readProgress(cap.user_id,plan);box.innerHTML=Decision.renderPreparation(plan,{esc,D},done);
            for(const b of box.querySelectorAll('[data-supplement]'))b.onclick=()=>supplement(b.dataset.supplement);
            for(const input of box.querySelectorAll('[data-prep-task]'))input.onchange=()=>{
              if(input.checked)done.add(input.dataset.prepTask);else done.delete(input.dataset.prepTask);
              input.closest('.prep-task').classList.toggle('is-done',input.checked);
              box.querySelector('[data-prep-count]').textContent=`已完成 ${done.size} / ${plan.tasks.length}`;
              if(!Decision.saveProgress(cap.user_id,plan,done))box.querySelector('[data-prep-storage]').textContent='浏览器未允许保存，勾选仅在本次打开时有效。';
            };
            const analyze=box.querySelector('[data-prep-analyze]');if(analyze){analyze.disabled=!eligible(row);analyze.onclick=()=>{opened.element.close();requestAnalysis([id]);};}
          }catch(err){if(current()&&U.drawerCurrent(opened.revision)){box.innerHTML=`<p class="pending-note">${esc(err.message)}</p><button class="btn" data-prep-retry>重试读取清单</button>`;box.querySelector('[data-prep-retry]').onclick=()=>{preparationLoaded=false;loadPreparation();};}}
        };
        opened.element.querySelector('#drawer-tab-preparation').addEventListener('click',loadPreparation);
        opened.element.querySelector('.detail-tabs').addEventListener('keydown',()=>setTimeout(()=>{if(opened.element.querySelector('#drawer-tab-preparation')?.getAttribute('aria-selected')==='true')loadPreparation();},0));
        for(const [key] of tabs)opened.element.querySelector('#drawer-tab-'+key).addEventListener('click',()=>{lastTab=key;saveBrowse();});
        if(lastTab!=='overview')U.activateTab(opened.element.querySelector('#drawer-tab-'+lastTab));
        if(lastTab==='preparation')await loadPreparation();
        if(!current()||!U.drawerCurrent(opened.revision))return;
        bindHeader();
        for(const b of opened.element.querySelectorAll('[data-detail-record]'))b.onclick=()=>openRecord(id).catch(err=>{document.querySelector('#notice').textContent=err.message;});
      }catch(err){if(valid()){loading=false;opened.element.innerHTML=header()+`<div class="drawer-body"><p class="pending-note">${esc(err.message)}</p></div>`;bindHeader();opened.element.querySelector('.drawer-body').insertAdjacentHTML('beforeend','<button class="btn" data-detail-retry>重新读取岗位详情</button>');opened.element.querySelector('[data-detail-retry]').onclick=()=>openMatchDetail(id,lastTab);}}
    }
    function comparisonHTML(){
      if(panel!=='company')return '';
      const companies=[...new Set(snapshot.jobs.map(j=>j.job.company))];
      return `<dialog id="match-comparison-dialog" class="modal comparison-modal" aria-labelledby="match-comparison-title">${U.modalHead('match-comparison-title','同公司岗位对比','复用分析结果，找到值得优先投递的方向。')}<div class="modal-body"><form id="match-comparison-form" class="comparison-controls"><label>公司<select id="match-comparison-company">${companies.map(c=>`<option value="${esc(c)}" ${c===comparisonCompany?'selected':''}>${esc(c)}</option>`).join('')}</select></label><label>对比范围<select id="match-comparison-scope">${[['ALL','该公司全部本地岗位'],['FILTERED','该公司当前筛选结果'],['SELECTED','该公司已选岗位']].map(([key,n])=>`<option value="${key}" ${key===comparisonScope?'selected':''}>${n}</option>`).join('')}</select></label><button class="btn btn-primary" ${comparisonBusy?'disabled':''}>${comparisonBusy?'正在读取…':comparisonReport?'刷新对比':'查看对比'}</button></form><p class="form-note">单次最多 200 个岗位。只读取本地已有分析，不增加模型调用或自动加入投递计划。</p><div aria-live="polite">${comparisonError?`<p class="pending-note">${esc(comparisonError)}</p>`:''}${comparisonReport?Decision.renderCompany(comparisonReport,{esc,D}):!comparisonBusy?'<div class="empty-state"><h3>选好范围，再查看对比</h3><p>待分析与过期岗位会一并标出，避免把局部结果当成全公司结论。</p></div>':'<p class="empty">正在读取当前资料与岗位分析…</p>'}</div></div></dialog>`;
    }
    function openComparison(scope){
      const jobs=chosen(),companies=[...new Set(jobs.map(j=>j.job.company))];
      comparisonCompany=scope==='SELECTED'&&companies.length===1?companies[0]:companyFilter||companies[0]||snapshot.jobs[0]?.job.company||'';
      comparisonScope=scope||'ALL';comparisonReport=null;comparisonError='';comparisonBusy=false;comparisonRevision++;panel='company';render();
    }
    async function loadComparison(){
      if(comparisonBusy||!current()||panel!=='company')return;
      const jobs=(comparisonScope==='SELECTED'?chosen():rows()).filter(j=>j.job.company===comparisonCompany);
      if(comparisonScope!=='ALL'&&!jobs.length){comparisonError='此公司在所选范围没有岗位，请更换范围或先选择岗位。';render();return;}
      if((comparisonScope==='ALL'?snapshot.jobs.filter(j=>j.job.company===comparisonCompany):jobs).length>200){comparisonError='对比最多 200 个岗位，请用筛选或勾选缩小范围。';render();return;}
      const revision=++comparisonRevision;comparisonBusy=true;comparisonError='';comparisonReport=null;render();
      try{const report=await api('/api/matching/company','POST',{...identity(),company:comparisonCompany,scope:comparisonScope,...(comparisonScope==='ALL'?{}:{job_ids:jobs.map(j=>j.job.id)})});if(!current()||panel!=='company'||revision!==comparisonRevision)return;comparisonReport=report;}
      catch(err){if(!current()||panel!=='company'||revision!==comparisonRevision)return;comparisonError=err.message;}
      finally{if(current()&&panel==='company'&&revision===comparisonRevision){comparisonBusy=false;render();}}
    }
    async function prepareExport(){
      if(preparing||running||bulkBusy||!current()||!selected.size)return;
      const jobs=chosen();
      if(jobs.length>1000){notice='每次最多导出 1,000 个岗位，请减少选择。';render();return;}
      const missing=jobs.filter(j=>!j.text_bytes);
      if(missing.length){notice=`已选岗位中有 ${missing.length} 个缺少最近可用原文，请在已选清单中移除后再导出。`;render();return;}
      preparing=true;clearExport();notice='正在从本机资料准备分析包，不调用外部模型。';render();
      try{
        const payload=await api('/api/matching/export','POST',{...identity(),job_ids:jobs.map(j=>j.job.id),candidate_hash:snapshot.candidate_hash});
        if(!current())return;
        exportFiles=C.makeFiles(payload);panel='export';exportKeys=new Map(jobs.map(j=>[j.job.id,exportKey(j)]));
        notice=`已准备 ${exportFiles.length} 个分析包，请逐包检查完整文字后下载。`;
      }catch(err){notice=err.message;}finally{preparing=false;render();}
    }
    async function refresh(){
      const next=await api('/api/matching/preview','POST',identity());
      const nextByID=new Map(next.jobs.map(j=>[j.job.id,j]));
      if(exportFiles.length&&(next.candidate_hash!==snapshot.candidate_hash||[...exportKeys].some(([id,key])=>!nextByID.has(id)||exportKey(nextByID.get(id))!==key))){clearExport();notice='资料或已选岗位已有变化，请重新准备分析包。';}
      C.pruneSelection(selected,next.jobs);C.storeSelection(cap.user_id,selected);
      if(next.candidate_hash!==snapshot.candidate_hash){consentHash='';paused=true;progress=readProgress(cap.user_id,next.candidate_hash,modelKey());autoQueue=[];notice='求职资料已变化，请重新核对外发资料。';}
      for(const j of next.jobs){if(baseline.has(j.job.id)&&baseline.get(j.job.id)!==j.input_key||!baseline.has(j.job.id)){if(eligible(j)&&!autoQueue.includes(j.job.id))autoQueue.push(j.job.id);}baseline.set(j.job.id,j.input_key);}
      const previousDetail=snapshot.jobs.find(j=>j.job.id===lastJob),nextDetail=next.jobs.find(j=>j.job.id===lastJob);
      snapshot=next;
      if(drawerOpen&&lastOpened&&U.drawerCurrent(lastOpened.revision)){
        if(!nextDetail)lastOpened.element.close();
        else if(previousDetail?.input_key!==nextDetail.input_key||previousDetail?.state!==nextDetail.state)await openMatchDetail(lastJob,lastTab,false);
      }
      reconcileProgress(progress,snapshot.jobs);persist();
    }
    async function taskControl(action){
      if(!task)return;const id=task.id;
      try{const latest=await api('/api/matching/tasks/'+encodeURIComponent(id));const v=await api('/api/matching/tasks/'+encodeURIComponent(id)+'/control','POST',{version:latest.version,action});if(!current())return;applyTask(v);notice=action==='CANCEL'?'本轮已取消，已保存结果仍可查看。':'当前批次结束后暂停后续分析。';render();}
      catch(e){if(current()){notice=e.message;render();}}
    }
    async function start(ids,retry=false,continuing=false){
      if(running||preparing||bulkBusy||!authorized()||!current())return;
      if(!root.CampusModels.available(cap)){notice='请先在模型设置填写密钥并选择模型。';render();return;}
      const idSet=new Set(ids),byID=new Map(snapshot.jobs.map(j=>[j.job.id,j]));const jobs=[...idSet].map(id=>byID.get(id)).filter(j=>j&&eligible(j));
      if(!jobs.length){notice='这批岗位已经分析完成，或暂时没有可分析的岗位。';render();return;}
      const workIDs=new Set(jobs.map(j=>j.job.id));
      if(durable){
        running=true;notice='正在保存本轮任务…';render();
        try{
          const resume=(retry||continuing)&&task&&!['COMPLETED','CANCELLED'].includes(task.state)&&task.candidate_hash===snapshot.candidate_hash&&[...workIDs].every(id=>task.items.some(i=>i.job_id===id&&i.input_key===jobs.find(j=>j.job.id===id).input_key));
          const target=resume?'/api/matching/tasks/'+encodeURIComponent(task.id)+'/resume':'/api/matching/tasks';
          const requestKey=root.crypto.randomUUID().replaceAll('-','');
          const body={job_ids:[...workIDs],input_keys:Object.fromEntries(jobs.map(j=>[j.job.id,j.input_key])),candidate_hash:snapshot.candidate_hash,mask_name:maskName,model_config:root.CampusModels.requestConfig(),request_key:requestKey,version:resume?task.version:0,retry};
          let v;try{v=await api(target,'POST',body);}catch(error){
            // Resolve an ambiguous response by reading server history, never by
            // automatically making another paid submission.
            if(!error.code){const runs=await api('/api/matching/tasks');const existing=runs.find(x=>resume?x.id===task.id&&Tasks.active(x):x.request_key===requestKey);if(existing)v=existing;else throw error;}else throw error;
          }
          if(!current())return;applyTask(v);notice='本轮任务已保存，可以离开页面；处理进度会自动更新。';
        }catch(e){running=false;notice=e.message;}finally{if(current())render();}return;
      }
      progress=queueWork(progress,[...workIDs],continuing);persist();
      let evidenceReviews=0;const reviewNotice=()=>evidenceReviews?` ${evidenceReviews} 项结论已本地复核，请打开岗位的逐项依据核对。`:'';
      running=true;paused=false;notice='正在分析，结果会逐批保存。';render();
      try{
        while(progress.pending.some(id=>workIDs.has(id))&&!paused&&current()&&authorized()){
          if(snapshot.calls_today>=snapshot.settings.daily_calls){notice='已达到每日岗位匹配调用上限，未完成项保留供之后继续。';paused=true;break;}
          const byID=new Map(snapshot.jobs.map(j=>[j.job.id,j]));const pending=progress.pending.filter(id=>workIDs.has(id)).map(id=>byID.get(id)).filter(j=>j&&eligible(j));
          if(!pending.length)break;
          if(pending[0].text_bytes>24000||!pending[0].text_bytes){progress.failed.push({id:pending[0].job.id,message:'岗位文字过长或原文尚不可用，请检查岗位内容。'});progress.pending=progress.pending.filter(id=>id!==pending[0].job.id);persist();continue;}
          const batch=pack(pending,retry?1:3),batchIDs=batch.map(j=>j.job.id);let batchFailed=false;
          try{
            const result=await api('/api/matching/analyze','POST',{job_ids:batchIDs,candidate_hash:snapshot.candidate_hash,mask_name:maskName,model_config:root.CampusModels.requestConfig()});
            const done=new Set([...(result.analyzed||[]),...(result.reused||[])]);
            progress.pending=progress.pending.filter(id=>!done.has(id));progress.done+=done.size;progress.calls+=result.calls||0;evidenceReviews+=result.evidence_reviews||0;
            notice=`本轮完成 ${progress.done} 个岗位，调用 ${progress.calls} 次；命中缓存的岗位不重复调用。`+reviewNotice();
          }catch(err){
            batchFailed=true;
            if(['MATCH_DAILY_LIMIT','MATCH_BUSY','MATCH_INPUT_CHANGED','MODEL_AUTH_FAILED','MODEL_BALANCE_LOW','MODEL_PROVIDER_BUSY'].includes(err.code)){notice=err.message;paused=true;if(err.code==='MATCH_INPUT_CHANGED')consentHash='';}
            else{for(const id of batchIDs)progress.failed.push({id,message:err.message});progress.pending=progress.pending.filter(id=>!batchIDs.includes(id));notice='部分岗位分析失败，已保留成功结果，可单独重试失败项。';}
          }
          const priorCalls=snapshot.calls_today;persist();await refresh();
          if(batchFailed)progress.calls+=Math.max(0,snapshot.calls_today-priorCalls);persist();render();
        }
        if(!progress.pending.length&&!progress.failed.length)notice=`本轮已完成 ${progress.done} 个岗位；可继续分析下一批。`+reviewNotice();
      }catch(err){notice=err.message;}finally{running=false;persist();render();}
    }
    render();
    if(current())Navigation?.register({active:current,checkpoint});
    async function pollTask(){
      if(!current()||!durable)return;
      try{if(task&&Tasks.active(task)){const v=await api('/api/matching/tasks/'+encodeURIComponent(task.id));if(!current())return;const oldDone=progress.done;applyTask(v);if(progress.done!==oldDone||!Tasks.active(v)){await refresh();applyTask(v);notice=Tasks.active(v)?'成功结果已保存。':v.state==='WAITING_AUTH'?'本轮已停止，请核对设置与外发资料后继续。':'本轮进度已保存。';}if(!panel&&!(document.querySelector('#job-drawer')?.open&&!document.querySelector('#job-drawer')?.classList.contains('is-docked'))&&!['INPUT','SELECT','TEXTAREA'].includes(document.activeElement?.tagName))render();}}
      catch(e){if(current()){notice=e.message;if(!panel)render();}}
      if(current())setTimeout(pollTask,5000);
    }
    if(durable)setTimeout(pollTask,5000);
    if(current()){if(initialJob){if(initialAnalyze)await requestAnalysis([initialJob]);else await openMatchDetail(initialJob,initialView);}else if(drawerOpen&&lastJob)await openMatchDetail(lastJob,lastTab,false);else if(!initialTask&&document.querySelector('#job-drawer')&&rows().length)await openMatchDetail(rows()[(pageNo-1)*50]?.job.id||rows()[0].job.id,'overview',false);}
    async function poll(){if(!current())return;try{if(!running&&!preparing&&!bulkBusy&&!panel&&!exportFiles.length&&!(document.querySelector('#job-drawer')?.open&&!document.querySelector('#job-drawer')?.classList.contains('is-docked'))&&!['INPUT','SELECT','TEXTAREA'].includes(document.activeElement?.tagName)){await refresh();render();if(snapshot.settings.auto_new&&!paused&&authorized()&&autoQueue.length){const ids=autoQueue.splice(0,snapshot.settings.round_limit);await start(ids);}}}catch(err){notice=err.message;render();}if(current())setTimeout(poll,60000);}
    setTimeout(poll,60000);
  }
  const api={page,showJob,pack,shortlist,filtered,directionMatch,catalogVisible,bulkTargets,workflowMatch,workflowHTML,journeyHTML,jobRowHTML,overviewHTML,renderResult,renderLocal,drawerActions,readProgress,reconcileProgress,queueWork,analysisActions,lock,bindUser,matchIdentity:identity};
  if(typeof module==='object'&&module.exports)module.exports=api;
  root.CampusMatching=api;return api;
})(typeof window==='undefined'?globalThis:window);
