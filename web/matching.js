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
    return `<h4>本地初筛依据</h4><p><strong>${esc(tiers[v.tier]||'信息不足')}</strong> · 优先级 ${Number(v.score).toFixed(1)} / 100</p><p class="meta">方向最高 30 分，城市与岗位类型偏好 20 分，技术线索 30 分，已确认的项目实现依据 20 分。必需项权重 3，工作内容 2，加分项 1。此处只核对文字线索，不代表掌握程度或录用概率；更新时间仅用于同分排序。</p>${v.role?`<p>识别方向：${esc(v.role)} · ${v.role_source==='BODY'?'来自岗位职责':'来自岗位标题'}</p><blockquote>${esc(v.role_excerpt)}</blockquote>`:''}<ul>${(v.reasons||[]).map(r=>`<li>${esc(r)}</li>`).join('')}</ul>${row.excluded_reason?`<p class="pending-note">${esc(row.excluded_reason)}</p>`:''}${(v.warnings||[]).map(w=>`<p class="pending-note">${esc(w)}</p>`).join('')}${v.checks?.length?`<details><summary>核对技术与项目线索（${v.checks.length} 项）</summary><div class="table-wrap"><table><thead><tr><th>岗位要求与原文</th><th>我的资料依据</th><th>初筛结果</th></tr></thead><tbody>${v.checks.map(c=>`<tr><td><span class="pill">${esc(labels[c.category]||c.category)}</span><p>${esc(c.terms.join(c.mode==='ANY'?' / ':' + '))} · ${c.mode==='ANY'?'任选一项':'逐项核对'}</p><blockquote>${esc(c.excerpt)}</blockquote></td><td>${c.evidence?.length?c.evidence.map(e=>`<p><small>${esc(labels[e.kind]||'资料依据')}</small><br>${esc(e.excerpt)}</p>`).join(''):'资料中暂无依据，可补充求职资料后核对'}</td><td>${esc(result[c.result]||'资料中暂无依据')}</td></tr>`).join('')}</tbody></table></div></details>`:''}`;
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
    const summary=sections.length?`<article class="card"><h4>分析摘要</h4><p>${esc(coreNote)}</p><p>投递资格：${esc(D.label('eligibility',r.qualifications?.status))}。城市与岗位类型按已保存偏好核对。</p>${core?.missing?'<p class="pending-note">可补充核心要求相关的具体项目事实，再重新核对；暂无依据不代表你不会。</p>':''}<div class="table-wrap"><table><thead><tr><th>核对部分</th><th>有依据／总项数</th><th>依据覆盖度</th></tr></thead><tbody>${sections.map(s=>`<tr><td>${esc(sectionLabels[s.category]||s.category)}</td><td>${s.known} / ${s.total}</td><td>${s.total?Number(s.coverage).toFixed(1)+'%':'暂无条目'}</td></tr>`).join('')}</tbody></table></div></article>`:'';
    return `<p class="match-score">核心技术匹配度：<strong>${esc(score)}</strong> · 核心要求依据覆盖度 ${Number(r.coverage).toFixed(1)}%</p><p class="meta">主分数只计算有依据的必需技术要求：直接匹配 1、部分匹配 0.5、可迁移经验 0.25、明确不符合 0。核心覆盖不足 60% 时不显示分数。工作内容、加分项、软性要求单独展示；明确任选的同组方向只计一项。分数不代表录用概率，也不改变招聘状态核验结果。</p><p class="meta">分析于 ${esc(D.date(r.analyzed_at))} · ${esc(r.model.split('\n').pop()||'所选模型')}</p>${summary}${renderRequirements(r,helpers)}<details><summary>查看本轮提取的资格核对</summary><p>根据明确条件与已保存资料计算；城市名称已归一，优先专业不作为硬门槛，要求缺失或冲突仍待核验。</p><div class="table-wrap"><table><thead><tr><th>核对项</th><th>岗位要求</th><th>我的情况</th><th>结果</th></tr></thead><tbody>${(r.qualifications?.results||[]).map(row=>`<tr><td>${esc(D.label('evidence',row.rule))}</td><td>${esc(D.requirement(row.requirement,row.rule))}</td><td>${esc(D.requirement(row.candidate_value,row.rule,'candidate_value'))}</td><td>${esc(D.label('rule',row.result))}</td></tr>`).join('')}</tbody></table></div></details>`;
  }
  async function showJob(box,id,helpers){
    const {api,navigate}=helpers;
    const cap=await api('/api/profile/resume/capabilities');root.CampusModels.bindUser(cap.user_id);bindUser(cap.user_id);
    const result=await api('/api/matching/results/'+encodeURIComponent(id),'POST',identity());
    if(!box.isConnected)return;
    box.innerHTML=`<h3>技能与项目匹配</h3>${renderLocal(result,helpers)}<h4>模型深度分析</h4>${renderResult(result,helpers)}<button type="button" data-open-matching>前往岗位匹配</button>`;
    box.querySelector('[data-open-matching]').onclick=()=>navigate('matching');
  }
  async function page(set,heading,{api,esc,D,UserError,navigate,active}){
    const cap=await api('/api/profile/resume/capabilities');root.CampusModels.bindUser(cap.user_id);bindUser(cap.user_id);
    let snapshot=await api('/api/matching/preview','POST',identity());
    let query='',filter='',tierFilter='',sort='local',pageNo=1,running=false,paused=false,consentHash='',notice='';
    const C=root.CampusMatchingChat;
    const selected=C.readSelection(cap.user_id,snapshot.jobs);
    let onlySelected=false,preparing=false,exportFiles=[],exportIndex=0,exportConsent=false,exportKeys=new Map();
    const modelKey=()=>JSON.stringify([identity().model_url,identity().model_name,cap.model]);
    let progress=reconcileProgress(readProgress(cap.user_id,snapshot.candidate_hash,modelKey()),snapshot.jobs);
    storeProgress(cap.user_id,progress);
    const baseline=new Map(snapshot.jobs.map(j=>[j.job.id,j.input_key]));let autoQueue=[];
    const current=()=>active();
    function ordered(data){return sort==='deep'?[...data].sort((a,b)=>(b.score??-1)-(a.score??-1)||b.preliminary_score-a.preliminary_score):data;}
    function rows(){return ordered(filtered(snapshot.jobs,query,filter,tierFilter).filter(j=>!onlySelected||selected.has(j.job.id)));}
    const chosen=()=>C.selectedRows(ordered(snapshot.jobs),selected);
    const exportKey=j=>JSON.stringify([j.input_key,j.job,j.excluded_reason]);
    function clearExport(){exportFiles=[];exportIndex=0;exportConsent=false;exportKeys.clear();}
    function selectionChanged(){C.storeSelection(cap.user_id,selected);clearExport();render();}
    function selectionHTML(view,slice){
      const jobs=chosen(),canAnalyze=shortlist(jobs,snapshot.settings.round_limit).length;
      return `<article class="card matching-selection"><h3>已选 ${selected.size} 个岗位</h3><p class="meta">选择跨页保留，切换筛选条件不会清空；当前浏览器会话按账号保存。API 每轮最多处理 ${snapshot.settings.round_limit} 个待分析岗位，跳过已完成和不可分析项。ChatGPT 导出每次最多 1,000 个岗位、5 MB，已分析或有本地提醒的岗位也可手动比较。</p><div class="actions"><button id="match-select-page" ${preparing||!slice.length?'disabled':''}>选择本页（${slice.length}）</button><button id="match-select-all" ${preparing||!view.length?'disabled':''}>选择全部筛选结果（${view.length}）</button><button id="match-select-top" ${preparing?'disabled':''}>选择前 ${snapshot.settings.round_limit} 个待分析岗位</button><button id="match-clear-selection" ${preparing||!selected.size?'disabled':''}>清空选择</button><button id="match-show-selection" ${!selected.size?'disabled':''}>查看全部已选岗位</button></div>${jobs.length?`<details><summary>已选清单（${jobs.length} 个）</summary><ul>${jobs.slice(0,10).map(j=>`<li>${esc(D.text(j.job.company))} · ${esc(D.text(j.job.title))} <button type="button" data-match-remove="${esc(j.job.id)}" ${preparing?'disabled':''}>移除</button></li>`).join('')}</ul>${jobs.length>10?'<p class="meta">此处展示前 10 个，点击「查看全部已选岗位」可分页核对和移除。</p>':''}</details>`:''}<div class="actions"><button id="match-selected-run" ${running||preparing||!authorized()||!canAnalyze?'disabled':''}>用 API 分析已选岗位（本轮 ${canAnalyze} 个）</button><button id="match-export" ${running||preparing||!selected.size||selected.size>1000?'disabled':''}>${preparing?'正在准备脱敏分析包…':'准备 ChatGPT 分析包'}</button></div>${selected.size>1000?'<p class="pending-note">已选数量超过单次导出上限，请减少到 1,000 个以内；API 仍可分轮分析。</p>':''}</article>`;
    }
    function exportHTML(){
      if(!exportFiles.length)return '';
      return `<article id="match-export-review" class="card"><h3>核对 ChatGPT 分析包</h3><p>共 ${exportFiles.length} 包、${exportFiles.reduce((n,f)=>n+f.jobCount,0)} 个岗位。下面显示将下载的完整文字，可继续删除敏感内容；修改多包重复的资料时，请保持一致。核对后下载，再手动上传或粘贴到 ChatGPT。不需要 API 密钥，准备和下载不调用模型。聊天结果目前不会自动写回项目。</p><label>查看分析包<select id="match-export-file">${exportFiles.map((f,i)=>`<option value="${i}" ${i===exportIndex?'selected':''}>${esc(f.name)} · ${f.jobCount} 个岗位${f.oversized?' · 长岗位独立包':''}</option>`).join('')}</select></label><label>完整导出文字（可编辑）<textarea id="match-export-text" spellcheck="false">${esc(exportFiles[exportIndex].text)}</textarea></label><label class="check"><input id="match-export-consent" type="checkbox" ${exportConsent?'checked':''}> 我已核对全部分析包，确认可以手动发给 ChatGPT</label><div class="actions"><button id="match-export-download" ${exportConsent?'':'disabled'}>${exportFiles.length===1?'下载分析包（Markdown）':'下载全部分析包（ZIP）'}</button><button id="match-export-copy" ${exportConsent?'':'disabled'}>复制当前包</button><button id="match-export-close">收起分析包</button></div><p class="meta">按公司尽量放在同一包，每包使用相同的分析标准，最多 8 个岗位并按约 48 KB 完整文字分包；长岗位独立成包，原文不会截断。多包 ZIP 内附使用说明和汇总指令。</p></article>`;
    }
    const authorized=()=>consentHash===snapshot.candidate_hash;
    function persist(){storeProgress(cap.user_id,progress);}
    function render(){
      if(!current())return;
      const view=rows(),totalPages=Math.max(1,Math.ceil(view.length/50));pageNo=Math.min(pageNo,totalPages);const slice=view.slice((pageNo-1)*50,pageNo*50);
      const analyzed=snapshot.jobs.filter(j=>j.state==='ANALYZED').length;
      const availableIDs=new Set(snapshot.jobs.filter(eligible).map(j=>j.job.id));
      const actions=analysisActions(progress,{running,preparing,authorized:authorized(),modelAvailable:root.CampusModels.available(cap),callsToday:snapshot.calls_today,dailyCalls:snapshot.settings.daily_calls,runCount:shortlist(view,snapshot.settings.round_limit).length,pendingCount:progress.pending.filter(id=>availableIDs.has(id)).length,failedCount:progress.failed.filter(f=>availableIDs.has(f.id)).length});
      const actionAttributes=state=>`${state.disabled?'disabled':''} title="${esc(state.reason)}" aria-describedby="match-actions-help"`;
      if(!set(`${heading}<p>全部岗位先在本地初筛，结合标题、岗位职责、技术要求和已确认项目事实排序。点击岗位名称查看依据，按初筛建议挑选后可用 API 或导出到 ChatGPT 分析。信息不足的岗位仍保留供核对。</p><p class="meta">当前模型：${esc(root.CampusModels.label(cap))} · ${snapshot.jobs.length} 个岗位已初筛 · ${analyzed} 个已深度分析 · 今日调用尝试 ${snapshot.calls_today} / ${snapshot.settings.daily_calls} 次（北京时间每日重置）</p><button id="match-model" ${preparing?'disabled':''}>选择模型</button><form id="match-settings"><div class="form-grid"><label>每轮最多分析岗位数<input name="round_limit" type="number" min="1" max="100" required value="${snapshot.settings.round_limit}"></label><label>每日岗位匹配调用上限<input name="daily_calls" type="number" min="1" max="200" required value="${snapshot.settings.daily_calls}"></label></div><label class="check"><input name="auto_new" type="checkbox" ${snapshot.settings.auto_new?'checked':''}> 自动分析新增或变化岗位（核对外发资料后，在此页面开启期间运行）</label><button ${running||preparing?'disabled':''}>保存分析设置</button></form><article class="card"><h3>本轮外发资料</h3><p>发送所选岗位的脱敏招聘说明，以及下面的技能、资格和已确认项目事实。简历文件、账号邮箱、联系方式和项目链接不进入匹配请求。请检查项目文字中是否还有需要遮盖的姓名。</p><form id="match-mask"><label>补充遮盖姓名或称呼（可选）<input name="mask_name" value="${esc(maskName)}" maxlength="60" placeholder="仅用于本轮本机脱敏"></label><button ${running||preparing?'disabled':''}>更新脱敏预览</button></form><details><summary>查看外发资料（${snapshot.candidate.facts.length} 条）</summary><ul>${snapshot.candidate.facts.map(f=>`<li><b>${esc(labels[f.kind]||f.kind)}</b>：${esc(f.kind==='DEGREE'?D.label('degree',f.text):D.text(f.text))}</li>`).join('')}</ul></details><label class="check"><input id="match-consent" type="checkbox" ${authorized()?'checked':''}> 我已核对以上资料，同意将本轮脱敏资料发送给所选模型</label><p class="meta">模型 API 独立计费。缓存命中不发请求；失败或超时的调用尝试也计入每日上限。按顺序处理，每批最多 3 个岗位并限制文字量。保持此页面打开；暂停后当前批次完成，再停止后续请求。</p></article><form id="match-filter"><div class="form-grid"><label>公司或岗位<input name="q" value="${esc(query)}" placeholder="例如：小红书、后端"></label><label>分析状态<select name="state"><option value="">全部</option>${['BASIC','ANALYZED','STALE'].map(s=>`<option value="${s}" ${filter===s?'selected':''}>${labels[s]}</option>`).join('')}</select></label><label>初筛建议<select name="tier"><option value="">全部</option>${Object.entries(tiers).map(([key,label])=>`<option value="${key}" ${tierFilter===key?'selected':''}>${label}</option>`).join('')}</select></label><label>排序<select name="sort"><option value="local" ${sort==='local'?'selected':''}>本地初筛优先级</option><option value="deep" ${sort==='deep'?'selected':''}>已分析能力匹配度</option></select></label></div><label class="check"><input name="only_selected" type="checkbox" ${onlySelected?'checked':''}> 只看已选岗位</label><button>筛选岗位</button></form>${selectionHTML(view,slice)}${exportHTML()}<div class="actions"><button id="match-run" ${actionAttributes(actions.run)}>分析筛选结果中的前 ${snapshot.settings.round_limit} 个待分析岗位</button><button id="match-continue" ${actionAttributes(actions.continue)}>继续未完成的本轮（${progress.pending.length}）</button><button id="match-retry" ${actionAttributes(actions.retry)}>重试失败项（${progress.failed.length}）</button><button id="match-pause" ${running?'':'disabled'}>暂停后续分析</button><button id="match-refresh" ${running||preparing?'disabled':''}>刷新岗位与进度</button></div><p id="match-actions-help" class="meta">${esc(actions.reason||[actions.continue.reason,actions.retry.reason].filter(Boolean).join(' '))} 继续只处理未完成项；重试只处理失败项，逐个分析，并保留其他未完成项。重新进入页面后需要再次核对外发资料并勾选同意。</p><p role="status">${esc(notice||`本轮已完成 ${progress.done} 个，调用尝试 ${progress.calls} 次。`)}</p>${progress.failed.length?`<details><summary>查看失败项</summary><ul>${progress.failed.map(f=>`<li>${esc(snapshot.jobs.find(j=>j.job.id===f.id)?.job.title||'岗位')}：${esc(f.message)}</li>`).join('')}</ul></details>`:''}<p class="meta">筛选结果 ${view.length} 条 · 第 ${pageNo} / ${totalPages} 页。初筛排序只用于选择分析顺序；明确不符合、已关闭或已忽略的岗位仍保留展示。</p><div class="table-wrap"><table><thead><tr><th>选择</th><th>岗位／公司</th><th>初筛优先级</th><th>分析状态</th><th>核心技术匹配／覆盖度</th><th>备注</th></tr></thead><tbody>${slice.map(j=>`<tr><td><label class="check match-row-select"><input type="checkbox" data-match-select="${esc(j.job.id)}" aria-label="${esc('选择 '+D.text(j.job.company)+' '+D.text(j.job.title))}" ${selected.has(j.job.id)?'checked':''} ${preparing?'disabled':''}></label></td><td><button data-match-job="${esc(j.job.id)}">${esc(D.text(j.job.title))}</button><p>${esc(D.text(j.job.company))} · ${esc((j.job.locations||[]).map(D.text).join('、'))}</p><small>${esc(j.local?.role||'方向待核对')} · 更新 ${esc(D.date(j.job.updated_at))}</small></td><td><span class="pill">${esc(tiers[j.local?.tier]||'信息不足')}</span><p>${j.preliminary_score.toFixed(1)} / 100</p><small>${esc(j.local?.reasons?.[0]||'缺少可用原文')}</small></td><td>${esc(labels[j.state])}</td><td>${j.state==='ANALYZED'?`${j.score==null?'暂无法可靠评分':Number(j.score).toFixed(1)+' / 100'}<br><small>核心依据覆盖 ${Number(j.coverage).toFixed(1)}%</small>`:'待深度分析'}</td><td>${esc(j.excluded_reason||j.local?.warnings?.[0]||'可以加入分析')}</td></tr>`).join('')||'<tr><td colspan="6">没有找到相关岗位。</td></tr>'}</tbody></table></div><div class="actions"><button id="match-prev" ${pageNo===1?'disabled':''}>上一页</button><button id="match-next" ${pageNo===totalPages?'disabled':''}>下一页</button></div><div id="match-detail"></div>`))return;
      const $=s=>document.querySelector(s);
      $('#match-model').onclick=()=>{paused=true;navigate('models');};
      $('#match-consent').onchange=e=>{consentHash=e.target.checked?snapshot.candidate_hash:'';paused=!e.target.checked;render();};
      $('#match-settings').onsubmit=async e=>{e.preventDefault();if(!e.target.reportValidity())return;const f=new FormData(e.target);try{snapshot.settings=await api('/api/matching/settings','PUT',{round_limit:Number(f.get('round_limit')),daily_calls:Number(f.get('daily_calls')),auto_new:f.get('auto_new')==='on'});notice='分析设置已保存。';render();}catch(err){notice=err.message;render();}};
      $('#match-mask').onsubmit=async e=>{e.preventDefault();const name=String(new FormData(e.target).get('mask_name')||'').trim();if(name&&name.length<2){notice='请填写至少两个字的姓名或称呼。';render();return;}clearExport();maskName=name;consentHash='';try{await refresh();notice='脱敏预览已更新，请核对后勾选同意。';}catch(err){notice=err.message;}render();};
      $('#match-filter').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target);query=f.get('q');filter=f.get('state');tierFilter=f.get('tier');sort=f.get('sort');onlySelected=f.get('only_selected')==='on';pageNo=1;render();};
      $('#match-run').onclick=()=>start(shortlist(rows(),snapshot.settings.round_limit).map(j=>j.job.id));
      $('#match-select-page').onclick=()=>{C.addSelection(selected,slice);selectionChanged();};
      $('#match-select-all').onclick=()=>{C.addSelection(selected,view);selectionChanged();};
      $('#match-select-top').onclick=()=>{C.addSelection(selected,shortlist(view,snapshot.settings.round_limit));selectionChanged();};
      $('#match-clear-selection').onclick=()=>{selected.clear();selectionChanged();};
      $('#match-show-selection').onclick=()=>{onlySelected=true;query='';filter='';tierFilter='';pageNo=1;render();};
      $('#match-selected-run').onclick=()=>start(shortlist(chosen(),snapshot.settings.round_limit).map(j=>j.job.id));
      $('#match-export').onclick=prepareExport;
      for(const input of document.querySelectorAll('[data-match-select]'))input.onchange=()=>{if(input.checked)selected.add(input.dataset.matchSelect);else selected.delete(input.dataset.matchSelect);selectionChanged();};
      for(const button of document.querySelectorAll('[data-match-remove]'))button.onclick=()=>{selected.delete(button.dataset.matchRemove);selectionChanged();};
      if(exportFiles.length){
        $('#match-export-file').onchange=e=>{exportIndex=Number(e.target.value);render();};
        $('#match-export-text').oninput=e=>{exportFiles[exportIndex].text=e.target.value;exportConsent=false;$('#match-export-consent').checked=false;$('#match-export-download').disabled=true;$('#match-export-copy').disabled=true;};
        $('#match-export-consent').onchange=e=>{exportConsent=e.target.checked;$('#match-export-download').disabled=!exportConsent;$('#match-export-copy').disabled=!exportConsent;};
        $('#match-export-close').onclick=()=>{clearExport();render();};
        $('#match-export-download').onclick=async()=>{if(!exportConsent)return;try{await C.download(exportFiles);notice='分析包已下载，请手动上传到 ChatGPT。';}catch(err){notice='无法下载分析包，请重试或复制当前包。';}render();};
        $('#match-export-copy').onclick=async()=>{if(!exportConsent)return;try{await root.navigator.clipboard.writeText(exportFiles[exportIndex].text);notice='当前分析包已复制，可以粘贴到 ChatGPT。';}catch(err){notice='此浏览器暂不能自动复制，请从预览框手动复制完整文字。';}render();};
      }
      $('#match-continue').onclick=()=>start([...progress.pending],false,true);
      $('#match-retry').onclick=()=>start(progress.failed.map(f=>f.id).filter(id=>availableIDs.has(id)).slice(0,snapshot.settings.round_limit),true);
      $('#match-pause').onclick=()=>{paused=true;notice='已暂停后续分析，当前批次完成后停止。';render();};
      $('#match-refresh').onclick=async()=>{try{await refresh();notice='岗位与分析状态已刷新。';}catch(err){notice=err.message;}render();};
      $('#match-prev').onclick=()=>{pageNo--;render();};$('#match-next').onclick=()=>{pageNo++;render();};
      for(const b of document.querySelectorAll('[data-match-job]'))b.onclick=async()=>{const row=snapshot.jobs.find(j=>j.job.id===b.dataset.matchJob);const box=$('#match-detail');box.dataset.jobId=b.dataset.matchJob;box.innerHTML=`<article class="card"><h3>${esc(D.text(row?.job.title||'岗位匹配明细'))}</h3>${renderLocal(row,{esc})}<h4>模型深度分析</h4><div id="match-deep-detail">正在读取已保存结果…</div></article>`;box.scrollIntoView({behavior:'smooth'});try{const v=await api('/api/matching/results/'+b.dataset.matchJob,'POST',identity());if(!current()||!box.isConnected||box.dataset.jobId!==b.dataset.matchJob)return;box.querySelector('#match-deep-detail').innerHTML=renderResult(v,{esc,D});}catch(err){if(box.isConnected&&box.dataset.jobId===b.dataset.matchJob)box.querySelector('#match-deep-detail').textContent=err.message;}};
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
        exportFiles=C.makeFiles(payload);exportKeys=new Map(jobs.map(j=>[j.job.id,exportKey(j)]));
        notice=`已准备 ${exportFiles.length} 个分析包，请逐包检查完整文字后下载。`;
      }catch(err){notice=err.message;}finally{preparing=false;render();if(current())document.querySelector('#match-export-review')?.scrollIntoView({behavior:'smooth'});}
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
    async function poll(){if(!current())return;try{if(!running&&!preparing&&!exportFiles.length&&!['INPUT','SELECT','TEXTAREA'].includes(document.activeElement?.tagName)){await refresh();render();if(snapshot.settings.auto_new&&!paused&&authorized()&&autoQueue.length){const ids=autoQueue.splice(0,snapshot.settings.round_limit);await start(ids);}}}catch(err){notice=err.message;render();}if(current())setTimeout(poll,60000);}
    setTimeout(poll,60000);
  }
  const api={page,showJob,pack,shortlist,filtered,renderResult,renderLocal,readProgress,reconcileProgress,queueWork,analysisActions,lock};
  if(typeof module==='object'&&module.exports)module.exports=api;
  root.CampusMatching=api;return api;
})(typeof window==='undefined'?globalThis:window);
