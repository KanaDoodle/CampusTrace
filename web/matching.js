'use strict';
const CampusMatching=(function(root){
  const labels={BASIC:'仅完成初筛',ANALYZED:'已深度分析',STALE:'待更新',QUALIFICATION:'投递资格',REQUIRED:'必需能力',BONUS:'加分项',RESPONSIBILITY:'工作内容',DIRECT:'直接匹配',PARTIAL:'部分匹配',TRANSFERABLE:'可迁移经验',NO_EVIDENCE:'暂无依据',MISMATCH:'明确不符合',SKILL:'技能',LANGUAGE:'语言',IMPLEMENTED:'已确认项目事实',LIMITATION:'已确认局限',DEGREE:'学历',GRADUATION:'毕业届别',EXPERIENCE:'相关经验',MAJOR:'专业',ROLE:'意向职能',CITY_PREFERRED:'首选城市',CITY_ACCEPTABLE:'可接受城市',JOB_TYPE_PREFERENCE:'意向岗位类型'};
  const tiers={HIGH:'优先查看',POSSIBLE:'可能相关',UNCERTAIN:'信息不足',LOW:'暂不优先'};
  let maskName='',boundUser='';
  function bindUser(id){if(id!==boundUser){maskName='';boundUser=id;}}
  function lock(){maskName='';boundUser='';}
  function identity(){const m=root.CampusModels.requestConfig();return {model_url:m?.url||'',model_name:m?.model||'',mask_name:maskName};}
  function eligible(j){return !j.excluded_reason&&j.state!=='ANALYZED';}
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
  function renderLocal(row,{esc}){
    const v=row.local;
    if(!v)return `<p class="empty">${esc(row.excluded_reason||'岗位原文尚不可用，暂无法提供本地初筛依据。')}</p>`;
    const result={SIGNAL:'有资料线索',PARTIAL:'部分有线索',NO_EVIDENCE:'资料中暂无依据'};
    return `<h4>本地初筛依据</h4><p><strong>${esc(tiers[v.tier]||'信息不足')}</strong> · 优先级 ${Number(v.score).toFixed(1)} / 100</p><details><summary>查看初筛计算方式</summary><p class="meta">方向最高 30 分，城市与岗位类型偏好 20 分，技术线索 30 分，已确认的项目实现依据 20 分。必需项权重 3，工作内容 2，加分项 1。此处只核对文字线索，不代表掌握程度或录用概率；更新时间仅用于同分排序。</p></details>${v.role?`<p>识别方向：${esc(v.role)} · ${v.role_source==='BODY'?'来自岗位职责':'来自岗位标题'}</p><blockquote>${esc(v.role_excerpt)}</blockquote>`:''}<ul>${(v.reasons||[]).map(r=>`<li>${esc(r)}</li>`).join('')}</ul>${row.excluded_reason?`<p class="pending-note">${esc(row.excluded_reason)}</p>`:''}${(v.warnings||[]).map(w=>`<p class="pending-note">${esc(w)}</p>`).join('')}${v.checks?.length?`<details><summary>核对技术与项目线索（${v.checks.length} 项）</summary><div class="table-wrap"><table><thead><tr><th>岗位要求与原文</th><th>我的资料依据</th><th>初筛结果</th></tr></thead><tbody>${v.checks.map(c=>`<tr><td><span class="pill">${esc(labels[c.category]||c.category)}</span><p>${esc(c.terms.join(c.mode==='ANY'?' / ':' + '))} · ${c.mode==='ANY'?'任选一项':'逐项核对'}</p><blockquote>${esc(c.excerpt)}</blockquote></td><td>${c.evidence?.length?c.evidence.map(e=>`<p><small>${esc(labels[e.kind]||'资料依据')}</small><br>${esc(e.excerpt)}</p>`).join(''):'资料中暂无依据，可补充求职资料后核对'}</td><td>${esc(result[c.result]||'资料中暂无依据')}</td></tr>`).join('')}</tbody></table></div></details>`:''}`;
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
  function renderRequirements(r,{esc}){
    const byID=new Map((r.matches||[]).map(m=>[m.requirement_id,m]));
    const candidate=new Map((r.candidate_facts||[]).map(f=>[f.id,f]));
    if(!r.requirements?.length)return '<p class="empty">模型未提取到明确要求，因此没有生成能力评分。</p>';
    return `<div class="table-wrap"><table><thead><tr><th>岗位要求与原文</th><th>我的依据</th><th>匹配结论</th></tr></thead><tbody>${r.requirements.map(req=>{const m=byID.get(req.id);return `<tr><td><span class="pill">${esc(req.aspect==='SOFT'?'软性要求':labels[req.category])}</span>${req.group_id?'<span class="pill">同组任选方向</span>':''}<p>${esc(req.text)}</p><blockquote>${esc(req.excerpt)}</blockquote>${req.group_id?`<small>选择规则：${esc(req.group_excerpt)}；同组按最有依据的方向核对。</small>`:''}${req.confidence<.8?'<small>要求解释的置信度较低，需核对原文。</small>':''}</td><td>${m?.evidence?.length?m.evidence.map(e=>{const f=candidate.get(e.id);return `<p><small>${f?.project_name?esc(f.project_name)+' · ':''}${esc(labels[f?.kind]||'资料依据')}</small><br>${esc(e.excerpt)}</p>`;}).join(''):'资料中暂无足够依据'}</td><td><b>${esc(labels[m?.result]||'暂无依据')}</b><p>${esc(m?.explanation||'需要补充资料后核对。')}</p></td></tr>`;}).join('')}</tbody></table></div>`;
  }
  function renderResult(v,helpers){
    const {esc,D}=helpers;
    if(v.state!=='ANALYZED'||!v.result){
      if(v.state==='STALE'&&v.result)return `<p class="pending-note">分析规则、岗位、资料或模型选择已有变化，请重新分析。</p><details><summary>查看上次分析依据（已过期）</summary><p class="meta">分析于 ${esc(D.date(v.result.analyzed_at))}；旧结论保留供核对，不参与当前评分。</p>${renderRequirements(v.result,helpers)}</details>`;
      return `<p class="empty">${v.state==='STALE'?'分析规则、岗位、资料或模型选择已有变化，请重新分析。':'尚未深度分析，可在岗位匹配中查看本地初筛并加入分析。'}</p>`;
    }
    const r=v.result,sections=r.breakdown||[],core=sections.find(s=>s.category==='REQUIRED');
    const sectionLabels={REQUIRED:'核心技术要求',RESPONSIBILITY:'工作内容相关性',BONUS:'加分项',SOFT:'软性要求'};
    const score=r.score==null?'暂无法可靠评分':Number(r.score).toFixed(1)+' / 100';
    const coreNote=core?core.total?`核心技术要求共 ${core.total} 项：直接匹配 ${core.direct} 项，部分匹配 ${core.partial} 项，可迁移经验 ${core.transferable} 项，暂无充分依据 ${core.missing} 项，明确不符合 ${core.mismatch} 项。`:'岗位未提取到可独立核对的核心技术要求。':'';
    const summary=sections.length?`<section class="match-result-summary"><h4>分析摘要</h4><p>${esc(coreNote)}</p><p>投递资格：${esc(D.label('eligibility',r.qualifications?.status))}。城市与岗位类型按已保存偏好核对。</p>${core?.missing?'<p class="pending-note">可补充核心要求相关的具体项目事实，再重新核对；暂无依据不代表你不会。</p>':''}<details><summary>查看各部分的依据覆盖度</summary><div class="table-wrap"><table><thead><tr><th>核对部分</th><th>有依据／总项数</th><th>依据覆盖度</th></tr></thead><tbody>${sections.map(s=>`<tr><td>${esc(sectionLabels[s.category]||s.category)}</td><td>${s.known} / ${s.total}</td><td>${s.total?Number(s.coverage).toFixed(1)+'%':'暂无条目'}</td></tr>`).join('')}</tbody></table></div></details></section>`:'';
    return `<p class="match-score">核心技术匹配度：<strong>${esc(score)}</strong> · 核心要求依据覆盖度 ${Number(r.coverage).toFixed(1)}%</p><details><summary>评分口径与模型信息</summary><p class="meta">主分数只计算有依据的必需技术要求：直接匹配 1、部分匹配 0.5、可迁移经验 0.25、明确不符合 0。核心覆盖不足 60% 时不显示分数。工作内容、加分项、软性要求单独展示；明确任选的同组方向只计一项。分数不代表录用概率，也不改变招聘状态核验结果。</p><p class="meta">分析于 ${esc(D.date(r.analyzed_at))} · ${esc(r.model.split('\n').pop()||'所选模型')}</p></details>${summary}<details><summary>查看全部岗位要求与个人依据（${r.requirements?.length||0} 项）</summary>${renderRequirements(r,helpers)}</details><details><summary>查看本轮提取的资格核对</summary><p>根据明确条件与已保存资料计算；城市名称已归一，优先专业不作为硬门槛，要求缺失或冲突仍待核验。</p><div class="table-wrap"><table><thead><tr><th>核对项</th><th>岗位要求</th><th>我的情况</th><th>结果</th></tr></thead><tbody>${(r.qualifications?.results||[]).map(row=>`<tr><td>${esc(D.label('evidence',row.rule))}</td><td>${esc(D.requirement(row.requirement,row.rule))}</td><td>${esc(D.requirement(row.candidate_value,row.rule,'candidate_value'))}</td><td>${esc(D.label('rule',row.result))}</td></tr>`).join('')}</tbody></table></div></details>`;
  }
  async function showJob(box,id,helpers){
    const {api,navigate}=helpers;
    const cap=await api('/api/profile/resume/capabilities');root.CampusModels.bindUser(cap.user_id);bindUser(cap.user_id);
    const result=await api('/api/matching/results/'+encodeURIComponent(id),'POST',identity());
    if(!box.isConnected)return;
    box.innerHTML=`<h3>技能与项目匹配</h3>${renderLocal(result,helpers)}<h4>模型深度分析</h4>${renderResult(result,helpers)}<button type="button" data-open-matching>前往岗位匹配</button>`;
    box.querySelector('[data-open-matching]').onclick=()=>navigate('matching');
  }
  async function page(set,heading,{api,esc,D,UserError,navigate,openRecord,initialQuery='',active}){
    const cap=await api('/api/profile/resume/capabilities');root.CampusModels.bindUser(cap.user_id);bindUser(cap.user_id);
    let snapshot;try{snapshot=await api('/api/matching/preview','POST',identity());}catch(error){if(error.code!=='MATCH_PROFILE_REQUIRED')throw error;const inventory=await api('/api/jobs');if(!set(root.CampusUI.heading('岗位库','完善求职资料后，可按你的条件初筛与分析。')+'<div class="note-box"><p>先保存求职条件与技能，再开启岗位匹配。</p><button id="match-create-profile" class="btn btn-primary">完善求职资料</button></div>'+inventory.map(j=>`<article class="card"><h3>${esc(D.text(j.title))}</h3><p class="meta">${esc(D.text(j.company))} · ${esc((j.locations||[]).join('、'))}</p><button data-basic-job="${esc(j.id)}" class="btn">查看核验记录</button></article>`).join('')))return;document.querySelector('#match-create-profile').onclick=()=>navigate('profile');for(const b of document.querySelectorAll('[data-basic-job]'))b.onclick=()=>openRecord(b.dataset.basicJob).catch(err=>{document.querySelector('#notice').textContent=err.message;});return;}
    let query=initialQuery,filter='',tierFilter='',sort='local',pageNo=1,running=false,paused=false,consentHash='',notice='';
    const C=root.CampusMatchingChat,U=root.CampusUI;
    let companyFilter='',cityFilter='',panel='',intent=null,reviewBundle=null,reviewBusy=false,reviewError='',reviewRevision=0,searchTimer;
    let reviewKeys=new Map();
    const selected=C.readSelection(cap.user_id,snapshot.jobs);
    let onlySelected=false,preparing=false,exportFiles=[],exportIndex=0,exportConsent=false,exportKeys=new Map();
    const modelKey=()=>JSON.stringify([identity().model_url,identity().model_name,cap.model]);
    let progress=reconcileProgress(readProgress(cap.user_id,snapshot.candidate_hash,modelKey()),snapshot.jobs);
    storeProgress(cap.user_id,progress);
    const baseline=new Map(snapshot.jobs.map(j=>[j.job.id,j.input_key]));let autoQueue=[];
    const current=()=>active();
    function ordered(data){return sort==='deep'?[...data].sort((a,b)=>(b.state==='ANALYZED'?b.score??-1:-1)-(a.state==='ANALYZED'?a.score??-1:-1)||b.preliminary_score-a.preliminary_score):data;}
    const cityName=s=>String(s||'').replace(/市$/,'');
    function rows(){return ordered(filtered(snapshot.jobs,query,filter==='FAILED'?'':filter,tierFilter).filter(j=>(!companyFilter||j.job.company===companyFilter)&&(!cityFilter||(j.job.locations||[]).some(c=>cityName(c)===cityFilter))&&(filter!=='FAILED'||progress.failed.some(f=>f.id===j.job.id))&&(!onlySelected||selected.has(j.job.id))));}
    const chosen=()=>C.selectedRows(ordered(snapshot.jobs),selected);
    const exportKey=j=>JSON.stringify([j.input_key,j.job,j.excluded_reason]);
    function clearExport(){exportFiles=[];exportIndex=0;exportConsent=false;exportKeys.clear();}
    function selectionChanged(){C.storeSelection(cap.user_id,selected);clearExport();render();}
    function selectionHTML(){
      const jobs=chosen(),canAnalyze=shortlist(jobs,snapshot.settings.round_limit).length;
      return `<div class="selection-bar matching-selection" ${selected.size?'':'hidden'}><div><strong>${selected.size}</strong> 个岗位已选<button id="match-clear-selection" class="btn btn-subtle btn-small" ${preparing?'disabled':''}>清空</button><button id="match-show-selection" class="btn btn-subtle btn-small">查看已选</button></div><div><button id="match-export" class="btn" ${running||preparing||!selected.size||selected.size>1000?'disabled':''}>${U.icon('download')}${preparing?'准备分析包…':'导出到 ChatGPT'}</button><button id="match-selected-run" class="btn btn-primary" ${running||preparing||!canAnalyze?'disabled':''}>${U.icon('spark')}深度分析（${canAnalyze}）</button></div></div>${selected.size>1000?'<p class="pending-note">单次导出最多 1,000 个岗位，请减少选择；API 可分轮处理。</p>':''}`;
    }
    function exportHTML(){
      if(!exportFiles.length)return '';
      return `<dialog id="match-export-review" class="modal wide" aria-labelledby="match-export-title">${U.modalHead('match-export-title','核对 ChatGPT 分析包','检查完整文字后，再手动上传到聊天。')}<div class="modal-body"><p>共 ${exportFiles.length} 包、${exportFiles.reduce((n,f)=>n+f.jobCount,0)} 个岗位。下面显示将下载的完整文字，可继续删除敏感内容；修改多包重复的资料时，请保持一致。核对后下载，再手动上传或粘贴到 ChatGPT。不需要 API 密钥，准备和下载不调用模型。聊天结果目前不会自动写回项目。</p><label>查看分析包<select id="match-export-file">${exportFiles.map((f,i)=>`<option value="${i}" ${i===exportIndex?'selected':''}>${esc(f.name)} · ${f.jobCount} 个岗位${f.oversized?' · 长岗位独立包':''}</option>`).join('')}</select></label><label>完整导出文字（可编辑）<textarea id="match-export-text" spellcheck="false">${esc(exportFiles[exportIndex].text)}</textarea></label><label class="check"><input id="match-export-consent" type="checkbox" ${exportConsent?'checked':''}> 我已核对全部分析包，确认可以手动发给 ChatGPT</label><div class="actions"><button id="match-export-download" ${exportConsent?'':'disabled'}>${exportFiles.length===1?'下载分析包（Markdown）':'下载全部分析包（ZIP）'}</button><button id="match-export-copy" ${exportConsent?'':'disabled'}>复制当前包</button><button id="match-export-close">收起分析包</button></div><p class="meta">按公司尽量放在同一包，每包使用相同的分析标准，最多 8 个岗位并按约 48 KB 完整文字分包；长岗位独立成包，原文不会截断。多包 ZIP 内附使用说明和汇总指令。</p></div></dialog>`;
    }
    const authorized=()=>consentHash===snapshot.candidate_hash;
    function persist(){storeProgress(cap.user_id,progress);}
    function settingsHTML(){return `<dialog id="match-settings-dialog" class="modal" aria-labelledby="match-settings-title">${U.modalHead('match-settings-title','分析设置','预算与处理方式集中在这里维护。')}<form id="match-settings"><div class="modal-body"><div class="form-grid"><label>每轮最多分析岗位数<input name="round_limit" type="number" min="1" max="100" required value="${snapshot.settings.round_limit}"></label><label>每日岗位匹配调用上限<input name="daily_calls" type="number" min="1" max="200" required value="${snapshot.settings.daily_calls}"></label></div><label class="check"><input name="auto_new" type="checkbox" ${snapshot.settings.auto_new?'checked':''}>自动分析新增或变化岗位</label><p class="form-note">自动处理只在本页面开启、本轮资料已核对且已经确认分析后运行。缓存命中不发请求，失败或超时的尝试也计入上限。每天按北京时间重置。</p><details><summary>查看调用与暂停规则</summary><p class="form-note">顺序处理，每批最多 3 个岗位并限制文字量。暂停后当前批次完成，再停止后续请求。关闭页面后未完成项保留供之后继续。</p></details>${notice?`<p class="modal-notice" role="status">${esc(notice)}</p>`:''}</div><div class="modal-foot"><small>模型 API 独立计费</small><button class="btn btn-primary" ${running||preparing?'disabled':''}>保存设置</button></div></form></dialog>`;}
    function reviewHTML(){
      const jobs=(intent?.ids||[]).map(id=>snapshot.jobs.find(j=>j.job.id===id)).filter(Boolean);
      return `<dialog id="match-review-dialog" class="modal wide" aria-labelledby="match-review-title">${U.modalHead('match-review-title','核对本轮外发资料','检查岗位与资料，再确认发起分析。')}<div class="modal-body"><h3>本轮 ${jobs.length} 个岗位</h3><div class="review-jobs">${jobs.map(j=>`<div class="review-job"><span>${esc(D.text(j.job.title))}</span><small>${esc(D.text(j.job.company))}</small></div>`).join('')}</div><p class="form-note">当前模型：${esc(root.CampusModels.label(cap))} <button type="button" id="match-model" class="text-btn">选择模型</button></p><p class="form-note">简历文件、账号邮箱、联系方式、项目链接和密钥不进入匹配文字。仍请核对项目文字里的姓名或称呼。</p><form id="match-mask"><label>补充遮盖姓名或称呼（可选）<input name="mask_name" value="${esc(maskName)}" maxlength="60" autocomplete="off" placeholder="仅用于本轮本机脱敏"></label><button class="btn btn-small" ${reviewBusy?'disabled':''}>更新脱敏预览</button></form><details class="disclosure" open><summary>候选人资料与项目事实（${snapshot.candidate.facts.length} 条）</summary><div><ul class="match-review-facts">${snapshot.candidate.facts.map(f=>`<li><b>${f.project_name?esc(f.project_name)+' · ':''}${esc(labels[f.kind]||f.kind)}</b>：${esc(f.kind==='DEGREE'?D.label('degree',f.text):D.text(f.text))}</li>`).join('')}</ul></div></details><details class="disclosure"><summary>所选岗位的完整外发文字</summary><div>${reviewBusy?'<p>正在从本地记录准备脱敏文字…</p>':reviewError?`<p class="pending-note">${esc(reviewError)}</p>`:(reviewBundle?.jobs||[]).map(j=>`<h3>${esc(j.title)}</h3><div class="drawer-original">${esc(j.text)}</div>`).join('<hr>')}</div></details>${reviewError?`<p class="pending-note">${esc(reviewError)}</p><button id="match-review-reload" class="btn btn-small">重新准备预览</button>`:''}<div class="note-box">点击「确认并开始分析」后才会调用模型。最多按当前每轮上限处理，成功结果逐批保存，失败项可单独重试。</div><label class="check-label"><input id="match-consent" type="checkbox" ${authorized()?'checked':''} ${reviewBusy||!reviewBundle?'disabled':''}>我已核对以上全部资料与岗位文字，同意将本轮脱敏资料发送给所选模型。</label></div><div class="modal-foot"><small>今日调用 ${snapshot.calls_today} / ${snapshot.settings.daily_calls}</small><div><button type="button" class="btn" data-dialog-close>取消</button><button id="match-confirm" class="btn btn-primary" ${!authorized()||!reviewBundle||reviewBusy||!root.CampusModels.available(cap)||snapshot.calls_today>=snapshot.settings.daily_calls?'disabled':''}>确认并开始分析</button></div></div></dialog>`;
    }
    async function requestAnalysis(ids,retry=false,continuing=false){
      if(running||preparing||!current())return;
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
      const hasQueue=running||progress.pending.length||progress.failed.length;
      if(!hasQueue&&!notice)return '';
      if(!hasQueue)return `<section class="match-progress match-progress-compact" aria-label="本轮分析任务"><p role="status">${esc(notice)}</p><button id="match-dismiss-notice" class="btn btn-subtle btn-small">知道了</button></section>`;
      return `<section class="match-progress" aria-label="本轮分析任务"><p role="status">${esc(notice||`本轮已完成 ${progress.done} 个，待处理 ${progress.pending.length} 个，失败 ${progress.failed.length} 个。`)}</p><div class="actions"><button id="match-continue" class="btn btn-small" ${actions.continue.disabled?'disabled':''} title="${esc(actions.continue.reason)}">继续未完成的本轮（${progress.pending.length}）</button><button id="match-retry" class="btn btn-small" ${actions.retry.disabled?'disabled':''} title="${esc(actions.retry.reason)}">重试失败项（${progress.failed.length}）</button><button id="match-pause" class="btn btn-small" ${running?'':'disabled'}>暂停后续分析</button></div><p id="match-actions-help" class="meta">${esc(actions.reason||[actions.continue.reason,actions.retry.reason].filter(Boolean).join(' '))} 点击继续或重试后，先核对本轮外发资料；重试逐个分析并保留其他未完成项。</p>${progress.failed.length?`<details><summary>查看失败项与原因</summary><ul>${progress.failed.map(f=>`<li>${esc(snapshot.jobs.find(j=>j.job.id===f.id)?.job.title||'岗位')}：${esc(f.message)}</li>`).join('')}</ul></details>`:''}</section>`;
    }
    function render(){
      if(!current())return;
      const saved=U.capture(),view=rows(),totalPages=Math.max(1,Math.ceil(view.length/50));pageNo=Math.min(pageNo,totalPages);
      const slice=view.slice((pageNo-1)*50,pageNo*50),availableIDs=new Set(snapshot.jobs.filter(eligible).map(j=>j.job.id));
      const actions=analysisActions(progress,{running,preparing,authorized:true,modelAvailable:root.CampusModels.available(cap),callsToday:snapshot.calls_today,dailyCalls:snapshot.settings.daily_calls,runCount:shortlist(view,snapshot.settings.round_limit).length,pendingCount:progress.pending.filter(id=>availableIDs.has(id)).length,failedCount:progress.failed.filter(f=>availableIDs.has(f.id)).length});
      const companies=[...new Set(snapshot.jobs.map(j=>j.job.company))],cities=[...new Set(snapshot.jobs.flatMap(j=>(j.job.locations||[]).map(cityName)))].sort();
      const activeCompany=!companyFilter?'全部岗位':companyFilter;
      const html=U.heading('岗位库','选出值得投的岗位，每个判断都有依据。',`<button id="match-source" class="btn" aria-label="关注来源">${U.icon('radar')}关注来源</button><button id="match-open-settings" class="btn" aria-label="分析设置">${U.icon('filter')}分析设置</button>`)+progressHTML(actions)+`
        <section class="workspace-panel" aria-label="岗位筛选与列表"><div class="company-tabs" aria-label="按公司筛选">${['',...companies].map(c=>`<button class="company-tab" data-match-company="${esc(c)}" aria-pressed="${companyFilter===c}">${esc(c||'全部岗位')}<span class="tab-count">${c?snapshot.jobs.filter(j=>j.job.company===c).length:snapshot.jobs.length}</span></button>`).join('')}</div>
        <form id="match-filter" class="matching-filter"><div class="form-grid"><label class="search-field">${U.icon('search')}<input id="match-search" name="q" type="search" aria-label="搜索公司或岗位" placeholder="搜索公司或岗位" value="${esc(query)}"></label><label><select id="match-city" name="city" aria-label="城市筛选"><option value="">所有城市</option>${cities.map(c=>`<option ${cityFilter===c?'selected':''}>${esc(c)}</option>`).join('')}</select></label><label><select id="match-state" name="state" aria-label="分析状态筛选"><option value="">所有状态</option>${Object.entries({BASIC:'待分析',ANALYZED:'已分析',STALE:'待更新',FAILED:'失败项'}).map(([s,n])=>`<option value="${s}" ${filter===s?'selected':''}>${n}</option>`).join('')}</select></label><label><select id="match-tier" name="tier" aria-label="初筛建议"><option value="">所有初筛建议</option>${Object.entries(tiers).map(([key,n])=>`<option value="${key}" ${tierFilter===key?'selected':''}>${n}</option>`).join('')}</select></label><label><select id="match-sort" name="sort" aria-label="岗位排序"><option value="local" ${sort==='local'?'selected':''}>初筛优先</option><option value="deep" ${sort==='deep'?'selected':''}>匹配度优先</option></select></label></div><div class="list-meta" style="padding:14px 0 0"><div><span>${view.length} 个岗位</span><label class="quiet-check"><input id="match-only-selected" name="only_selected" type="checkbox" ${onlySelected?'checked':''}>只看已选</label></div><button type="button" id="match-select-top" class="btn btn-subtle btn-small" ${preparing?'disabled':''}>${U.icon('plus')}选择优先分析岗位</button></div></form>
        <div class="selection-options"><button id="match-select-page" class="btn btn-subtle btn-small" ${preparing||!slice.length?'disabled':''}>选择本页（${slice.length}）</button><button id="match-select-all" class="btn btn-subtle btn-small" ${preparing||!view.length?'disabled':''}>选择全部结果（${view.length}）</button><button id="match-run" class="btn btn-subtle btn-small" ${actions.run.disabled?'disabled':''} title="${esc(actions.run.reason)}">分析前 ${snapshot.settings.round_limit} 个待分析岗位</button></div>
        <table class="job-table"><thead><tr><th class="check-cell" scope="col">选择</th><th class="job-cell" scope="col">岗位 / 工作地点</th><th class="tier-cell" scope="col">本地初筛</th><th class="match-cell" scope="col">核心技术匹配</th><th class="state-cell" scope="col">分析状态</th></tr></thead><tbody>${slice.map(j=>`<tr class="${selected.has(j.job.id)?'is-selected':''}"><td class="check-cell"><label class="check match-row-select"><input type="checkbox" data-match-select="${esc(j.job.id)}" aria-label="${esc('选择 '+D.text(j.job.company)+' '+D.text(j.job.title))}" ${selected.has(j.job.id)?'checked':''} ${preparing?'disabled':''}></label></td><td class="job-cell"><button class="job-title" data-match-job="${esc(j.job.id)}">${esc(D.text(j.job.title))}</button><div class="job-meta">${esc(D.text(j.job.company))}<span class="company-dot"></span>${esc((j.job.locations||[]).map(D.text).join(' / '))}<span class="company-dot"></span>${esc(D.label('job_type',j.job.job_type))}</div><span class="date-note">更新 ${esc(D.date(j.job.updated_at))}</span></td><td class="tier-cell"><span class="badge badge-${j.local?.tier==='HIGH'?'green':j.local?.tier==='POSSIBLE'?'blue':'gray'}">${esc(tiers[j.local?.tier]||'信息不足')}</span><span class="tier-note">${esc(j.local?.reasons?.[0]||j.excluded_reason||'缺少可用原文')}</span></td><td class="match-cell">${j.state==='ANALYZED'?`${j.score==null?'<span class="unscored">暂无法可靠评分</span>':`<span class="score">${Number(j.score).toFixed(1)}<small>/ 100</small></span>`}<span class="coverage">依据覆盖 ${Number(j.coverage).toFixed(1)}%</span>`:`<span class="unscored">${j.state==='STALE'?'依据变化，待更新':'待深度分析'}</span><span class="coverage">初筛不等同于匹配度</span>`}</td><td class="state-cell"><span class="badge badge-${progress.failed.some(f=>f.id===j.job.id)?'amber':j.state==='ANALYZED'?'green':j.state==='STALE'?'amber':'gray'}">${progress.failed.some(f=>f.id===j.job.id)?'分析失败':esc(labels[j.state])}</span>${j.excluded_reason?`<span class="date-note">${esc(j.excluded_reason)}</span>`:''}</td></tr>`).join('')||'<tr><td colspan="5"><div class="empty-state"><h3>没有找到符合条件的岗位</h3><p>试试其他关键词，或清除筛选条件。</p><button id="match-reset" class="btn">清除筛选</button></div></td></tr>'}</tbody></table>
        <div class="table-footer"><span>${U.icon('shield')}本地初筛无需模型 · 今日调用 ${snapshot.calls_today} / ${snapshot.settings.daily_calls}</span><span><button id="match-refresh" class="btn btn-small" ${running||preparing?'disabled':''}>刷新</button> 第 ${pageNo} / ${totalPages} 页 <button id="match-prev" class="btn btn-small" ${pageNo===1?'disabled':''}>上一页</button><button id="match-next" class="btn btn-small" ${pageNo===totalPages?'disabled':''}>下一页</button></span></div></section>${selectionHTML()}<p class="meta" style="margin:18px 0 30px">当前模型：${esc(root.CampusModels.label(cap))}。已关闭、已忽略或明确不符合的岗位保留供查看，分析时跳过。</p>${settingsHTML()}${reviewHTML()}${exportHTML()}`;
      if(!set(html))return;
      const $=s=>document.querySelector(s),on=(id,fn)=>{const el=$('#'+id);if(el)el.onclick=fn;};
      on('match-dismiss-notice',()=>{notice='';render();});on('match-source',()=>navigate('watches'));on('match-open-settings',()=>{panel='settings';render();});
      on('match-model',()=>{paused=true;navigate('models');});
      $('#match-consent').onchange=e=>{consentHash=e.target.checked?snapshot.candidate_hash:'';paused=true;render();};
      on('match-confirm',confirmAnalysis);on('match-review-reload',()=>requestAnalysis(intent.ids,intent.retry,intent.continuing));
      $('#match-settings').onsubmit=async e=>{e.preventDefault();if(!e.target.reportValidity())return;const f=new FormData(e.target);try{snapshot.settings=await api('/api/matching/settings','PUT',{round_limit:Number(f.get('round_limit')),daily_calls:Number(f.get('daily_calls')),auto_new:f.get('auto_new')==='on'});notice='分析设置已保存。';panel='';render();}catch(err){notice=err.message;render();}};
      $('#match-mask').onsubmit=async e=>{e.preventDefault();const name=String(new FormData(e.target).get('mask_name')||'').trim();if(name&&name.length<2){consentHash='';reviewError='请填写至少两个字的姓名或称呼。';render();return;}clearExport();maskName=name;consentHash='';const work=intent;try{await refresh();await requestAnalysis(work.ids,work.retry,work.continuing);}catch(err){reviewError=err.message;render();}};
      const applyFilter=()=>{const f=new FormData($('#match-filter'));query=String(f.get('q')||'');filter=String(f.get('state')||'');tierFilter=String(f.get('tier')||'');cityFilter=String(f.get('city')||'');sort=f.get('sort');onlySelected=f.get('only_selected')==='on';pageNo=1;render();};
      $('#match-filter').onsubmit=e=>{e.preventDefault();clearTimeout(searchTimer);applyFilter();};
      $('#match-filter').onchange=applyFilter;
      $('#match-search').oninput=()=>{clearTimeout(searchTimer);searchTimer=setTimeout(()=>{if(current())applyFilter();},240);};
      on('match-reset',()=>{query='';filter='';tierFilter='';cityFilter='';companyFilter='';onlySelected=false;pageNo=1;render();});
      for(const b of document.querySelectorAll('[data-match-company]'))b.onclick=()=>{companyFilter=b.dataset.matchCompany;pageNo=1;render();};
      on('match-run',()=>requestAnalysis(shortlist(rows(),snapshot.settings.round_limit).map(j=>j.job.id)));
      on('match-select-page',()=>{C.addSelection(selected,slice);selectionChanged();});on('match-select-all',()=>{C.addSelection(selected,view);selectionChanged();});on('match-select-top',()=>{C.addSelection(selected,shortlist(view,snapshot.settings.round_limit));selectionChanged();});
      on('match-clear-selection',()=>{selected.clear();selectionChanged();});on('match-show-selection',()=>{onlySelected=true;query='';filter='';tierFilter='';cityFilter='';companyFilter='';pageNo=1;render();});
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
      on('match-pause',()=>{paused=true;notice='已暂停后续分析，当前批次完成后停止。';render();});
      on('match-prev',()=>{pageNo--;render();});on('match-next',()=>{pageNo++;render();});
      for(const b of document.querySelectorAll('[data-match-job]'))b.onclick=()=>openMatchDetail(b.dataset.matchJob);
      const dialogID={settings:'match-settings-dialog',review:'match-review-dialog',export:'match-export-review'}[panel];
      if(dialogID){const dialog=$('#'+dialogID);U.openDialog(dialog,()=>{if(document.querySelector('#'+dialogID)!==dialog)return;panel='';if(dialogID==='match-review-dialog'){consentHash='';paused=true;reviewRevision++;reviewBusy=false;}});}
      U.restore(saved);
    }
    async function openMatchDetail(id){
      const row=snapshot.jobs.find(j=>j.job.id===id);if(!row)return;
      const header=`<div class="drawer-top"><div class="drawer-topline"><span>${esc(D.text(row.job.company))}</span><button type="button" class="icon-btn" data-dialog-close aria-label="关闭岗位详情">${U.icon('close')}</button></div><h2 id="drawer-title">${esc(D.text(row.job.title))}</h2><p class="drawer-subtitle">${esc((row.job.locations||[]).map(D.text).join(' / '))} · ${esc(D.label('job_type',row.job.job_type))}</p><div class="drawer-actions"><button class="btn btn-primary" data-detail-analyze ${eligible(row)?'':'disabled'}>${U.icon('spark')}${row.state==='STALE'?'更新分析':'深度分析'}</button><button class="btn" data-detail-plan>${U.icon('plus')}加入投递计划</button></div></div>`;
      const opened=U.drawer(header+'<div class="drawer-loading">正在读取本地保存的结果与岗位原文…</div>');
      const bindHeader=()=>{
        opened.element.querySelector('[data-detail-analyze]').onclick=()=>{opened.element.close();requestAnalysis([id]);};
        opened.element.querySelector('[data-detail-plan]').onclick=async e=>{e.target.disabled=true;try{const apps=await api('/api/applications');if(apps.some(a=>a.job_id===id)){document.querySelector('#notice').textContent='已有投递记录，可在投递进展中查看。';return;}await api('/api/applications','POST',{job_id:id});document.querySelector('#notice').textContent='已加入投递计划。';e.target.textContent='已加入投递计划';}catch(err){document.querySelector('#notice').textContent=err.message;e.target.disabled=false;}};
      };
      bindHeader();
      try{
        const [result,record]=await Promise.all([api('/api/matching/results/'+encodeURIComponent(id),'POST',identity()),api('/api/jobs/'+encodeURIComponent(id))]);
        if(!current()||!U.drawerCurrent(opened.revision))return;
        const observations=record.observations||[],latest=observations.find(o=>o.text)||null;
        const tabs=[['overview','匹配概览'],['evidence','逐项依据'],['source','岗位原文']];
        opened.element.innerHTML=header+`<div class="drawer-body"><div class="detail-tabs" role="tablist" aria-label="岗位详情视图">${tabs.map(([key,n],i)=>`<button id="drawer-tab-${key}" role="tab" aria-selected="${i===0}" aria-controls="drawer-${key}" tabindex="${i===0?0:-1}" data-view-tab>${n}</button>`).join('')}</div><div id="drawer-overview" role="tabpanel" aria-labelledby="drawer-tab-overview">${renderResult(result,{esc,D})}<details data-remember id="drawer-local"><summary>查看本地初筛依据</summary>${renderLocal(row,{esc})}</details><button class="btn btn-small" data-detail-record>查看完整岗位核验记录</button></div><div id="drawer-evidence" role="tabpanel" aria-labelledby="drawer-tab-evidence" hidden>${result.result?`${result.state==='STALE'?'<p class="pending-note">以下是过期结果，仅供核对。需要重新分析后才能用于当前判断。</p>':''}${renderRequirements(result.result,{esc})}`:'<p class="empty">尚未产生模型逐项分析，可先查看本地初筛依据。</p>'+renderLocal(row,{esc})}</div><div id="drawer-source" role="tabpanel" aria-labelledby="drawer-tab-source" hidden><p class="meta">最近可用岗位观察：${latest?esc(D.date(latest.observed_at)):'暂无记录'}。原文保留来源语言，不代表仍可投递。</p><div class="drawer-original">${esc(latest?.text||'目前没有可用的岗位原文。')}</div><button class="btn btn-small" data-detail-record>查看完整岗位核验记录</button></div></div>`;
        bindHeader();
        for(const b of opened.element.querySelectorAll('[data-detail-record]'))b.onclick=()=>openRecord(id).catch(err=>{document.querySelector('#notice').textContent=err.message;});
      }catch(err){if(U.drawerCurrent(opened.revision)){opened.element.innerHTML=header+`<div class="drawer-body"><p class="pending-note">${esc(err.message)}</p></div>`;bindHeader();}}
    }
    async function prepareExport(){
      if(preparing||running||!current()||!selected.size)return;
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
      snapshot=next;
      reconcileProgress(progress,snapshot.jobs);persist();
    }
    async function start(ids,retry=false,continuing=false){
      if(running||preparing||!authorized()||!current())return;
      if(!root.CampusModels.available(cap)){notice='请先在模型设置填写密钥并选择模型。';render();return;}
      const idSet=new Set(ids),byID=new Map(snapshot.jobs.map(j=>[j.job.id,j]));const jobs=[...idSet].map(id=>byID.get(id)).filter(j=>j&&eligible(j));
      if(!jobs.length){notice='这批岗位已经分析完成，或暂时没有可分析的岗位。';render();return;}
      const workIDs=new Set(jobs.map(j=>j.job.id));
      progress=queueWork(progress,[...workIDs],continuing);persist();
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
            progress.pending=progress.pending.filter(id=>!done.has(id));progress.done+=done.size;progress.calls+=result.calls||0;
            notice=`本轮完成 ${progress.done} 个岗位，调用 ${progress.calls} 次；命中缓存的岗位不重复调用。`;
          }catch(err){
            batchFailed=true;
            if(['MATCH_DAILY_LIMIT','MATCH_BUSY','MATCH_INPUT_CHANGED','MODEL_AUTH_FAILED','MODEL_BALANCE_LOW','MODEL_PROVIDER_BUSY'].includes(err.code)){notice=err.message;paused=true;if(err.code==='MATCH_INPUT_CHANGED')consentHash='';}
            else{for(const id of batchIDs)progress.failed.push({id,message:err.message});progress.pending=progress.pending.filter(id=>!batchIDs.includes(id));notice='部分岗位分析失败，已保留成功结果，可单独重试失败项。';}
          }
          const priorCalls=snapshot.calls_today;persist();await refresh();
          if(batchFailed)progress.calls+=Math.max(0,snapshot.calls_today-priorCalls);persist();render();
        }
        if(!progress.pending.length&&!progress.failed.length)notice=`本轮已完成 ${progress.done} 个岗位；可继续分析下一批。`;
      }catch(err){notice=err.message;}finally{running=false;persist();render();}
    }
    render();
    async function poll(){if(!current())return;try{if(!running&&!preparing&&!panel&&!exportFiles.length&&!document.querySelector('#job-drawer')?.open&&!['INPUT','SELECT','TEXTAREA'].includes(document.activeElement?.tagName)){await refresh();render();if(snapshot.settings.auto_new&&!paused&&authorized()&&autoQueue.length){const ids=autoQueue.splice(0,snapshot.settings.round_limit);await start(ids);}}}catch(err){notice=err.message;render();}if(current())setTimeout(poll,60000);}
    setTimeout(poll,60000);
  }
  const api={page,showJob,pack,shortlist,filtered,renderResult,renderLocal,readProgress,reconcileProgress,queueWork,analysisActions,lock};
  if(typeof module==='object'&&module.exports)module.exports=api;
  root.CampusMatching=api;return api;
})(typeof window==='undefined'?globalThis:window);
