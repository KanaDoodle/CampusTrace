'use strict';
const CampusCompanyDecision=(function(root){
  const states={ANALYZED:'已深度分析',BASIC:'待分析',STALE:'分析待更新'};
  const fitLabels={STRONG:'优先',RELATED:'相关',WEAK:'较弱',UNCERTAIN:'待核对'},fitOrder={STRONG:3,RELATED:2,WEAK:1,UNCERTAIN:0};
  const stateMemory=new Map(),stateKey=(user,company)=>'campustrace:company-picker:v1:'+encodeURIComponent(user)+':'+encodeURIComponent(company);
  function cleanState(v={}){
    const ids=value=>[...new Set((Array.isArray(value)?value:[]).filter(id=>typeof id==='string'&&id.length>0&&id.length<=128))].slice(0,200);
    return {selected:ids(v.selected),scope:ids(v.scope),selectedScope:v.selectedScope===true,search:typeof v.search==='string'?v.search.slice(0,200):'',state:['ANALYZED','BASIC','STALE'].includes(v.state)?v.state:'',fit:['STRONG','RELATED','WEAK','UNCERTAIN','RELEVANT'].includes(v.fit)?v.fit:'',city:typeof v.city==='string'?v.city.slice(0,100):'',sort:['deep','technical','local','title'].includes(v.sort)?v.sort:'deep',onlySelected:v.onlySelected===true,showQuotaFull:v.showQuotaFull===true,quickCount:[4,8,16].includes(v.quickCount)?v.quickCount:4,page:Number.isInteger(v.page)&&v.page>0?Math.min(v.page,334):1,scroll:Number.isFinite(v.scroll)?Math.max(0,Math.min(v.scroll,100000)):0};
  }
  function readState(user,company){let v=stateMemory.get(stateKey(user,company));try{const raw=root.sessionStorage?.getItem(stateKey(user,company));if(raw)v=JSON.parse(raw);}catch{}return cleanState(v);}
  function storeState(user,company,value){const v=cleanState(value),key=stateKey(user,company);stateMemory.set(key,v);try{root.sessionStorage?.setItem(key,JSON.stringify(v));}catch{}return v;}
  function rememberCompany(user,company){stateMemory.set(stateKey(user,''),company);try{root.sessionStorage?.setItem(stateKey(user,''),company);}catch{}}
  function lastCompany(user){try{return root.sessionStorage?.getItem(stateKey(user,''))||stateMemory.get(stateKey(user,''))||'';}catch{return stateMemory.get(stateKey(user,''))||'';}}
  function candidateScore(row,mode='deep'){
    if(mode==='local')return Number.isFinite(row.preliminary_score)?row.preliminary_score:null;
    if(row.state!=='ANALYZED')return null;
    if(mode==='technical')return Number.isFinite(row.score)?row.score:null;
    if(Object.hasOwn(fitOrder,row.fit))return fitOrder[row.fit];
    return Number.isFinite(row.priority?.score)?row.priority.score:Number.isFinite(row.score)?row.score:null;
  }
  function candidateRows(rows,options={},selected=new Set()){
    const words=String(options.search||'').trim().toLowerCase().split(/\s+/).filter(Boolean);
    const filtered=rows.filter(r=>(options.showQuotaFull||options.onlySelected||!r.campaign?.hide_unsubmitted)&&words.every(w=>(r.job.title+' '+(r.job.locations||[]).join(' ')).toLowerCase().includes(w))&&(!options.state||r.state===options.state)&&(!options.city||(r.job.locations||[]).includes(options.city))&&(!options.onlySelected||selected.has(r.job.id))&&(!options.fit||(r.state==='ANALYZED'&&(options.fit==='RELEVANT'?['STRONG','RELATED'].includes(r.fit):r.fit===options.fit))));
    return filtered.sort((a,b)=>{
      if(options.sort==='title')return a.job.title.localeCompare(b.job.title,'zh');
      // New qualitative assessments and historical numbers have different units.
      // Keep them in separate groups rather than comparing 3 with 90 points.
      if((options.sort||'deep')==='deep'){
        const group=r=>r.state!=='ANALYZED'?2:Object.hasOwn(fitOrder,r.fit)?0:candidateScore(r)==null?2:1;
        if(group(a)!==group(b))return group(a)-group(b);
      }
      const x=candidateScore(a,options.sort),y=candidateScore(b,options.sort);
      return (x==null?(y==null?0:1):y==null?-1:y-x)||a.job.title.localeCompare(b.job.title,'zh')||a.job.id.localeCompare(b.job.id);
    });
  }
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
      return `<article class="company-choice ${choice?.rank===1?'company-choice-first':''}"><div class="company-choice-head"><span class="company-rank">${choice?choice.rank:'—'}</span><div><span class="meta">${choice?choice.rank===1?'本次首选':choice.rank===2?'本次备选':'本次顺序':'未参与当前排序'}${c?.application?' · '+esc(D.label('application',c.application.current_state)):''}</span><h3>${esc(row.job.title)}</h3><p class="meta">${esc((row.job.locations||[]).join(' / ')||'地点待核对')} · ${esc(D.label('job',c?.current_status||row.job.current_status))}${c?.deadline?' · 截止 '+esc(c.deadline_date?c.deadline_date+'（当天结束）':D.date(c.deadline)): ' · 截止时间未明确'}</p></div></div>${choice?`<p class="company-choice-reason">${esc(choice.reason)}</p><details id="company-choice-${esc(id)}" data-remember class="company-choice-details"><summary>优势、取舍与依据</summary><dl class="company-tradeoffs"><div><dt>相对优势</dt><dd>${esc(choice.advantage)}</dd></div><div><dt>需要取舍</dt><dd>${esc(choice.tradeoff)}</dd></div></dl><blockquote>${esc(choice.job_excerpt)}</blockquote>${(choice.evidence||[]).map(e=>`<blockquote>${esc(e.excerpt)}</blockquote>`).join('')}</details>`:`<p>${esc(states[row.state]||'待核对')}${row.holistic?' · '+esc(root.CampusDecision.fitLabels[row.holistic.fit]):''}。${row.blocked_reason?esc(row.blocked_reason):'单岗结论可作参考，整体比较后才给出同公司顺序。'}</p>`}<div class="actions"><button class="btn btn-small ${plan.allowed?'btn-primary':''}" data-company-plan="${esc(id)}" ${!plan.allowed&&!c?.application?'disabled':''}>${esc(plan.label)}</button><button class="btn btn-small" data-company-detail="${esc(id)}">查看岗位</button>${row.state==='ANALYZED'?`<button class="text-btn" data-company-prepare="${esc(id)}">准备清单</button>`:''}${link?`<a class="text-btn" href="${esc(link)}" target="_blank" rel="noopener noreferrer">招聘官网</a>`:''}</div>${pending===id?`<div class="company-plan-confirm" role="region" aria-label="确认加入计划"><p>将「${esc(row.job.title)}」加入投递计划。${esc(plan.reason)}实际投递请前往招聘官网。</p><button class="btn btn-primary" data-company-plan-confirm="${esc(id)}">确认加入计划</button><button class="btn" data-company-plan-cancel>取消</button></div>`:''}</article>`;
    };
    return `<div class="company-decision-grid"><section class="company-ranking" aria-label="本次岗位取舍"><div class="company-section-heading"><h2>${report.holistic?'这次，优先考虑哪一份？':'先选候选岗位，再比较取舍'}</h2><p>${report.holistic?esc(report.holistic.summary):'已有单岗分析仍可查看。完整材料比较会进一步解释首选与备选。'}</p><p class="meta">${report.scope==='ALL'?'该公司全部本地岗位':'本次手动选择的岗位'} ${report.total} 个 · 当前排序 ${choices.length} 个 · 待分析 ${report.pending} · 分析待更新 ${report.stale}${report.holistic?' · '+(report.holistic.model?.startsWith('manual-chat')?'GPT 聊天导入':'模型比较')+' · 分析于 '+esc(D.date(report.holistic.analyzed_at)):''}。未覆盖官网全部岗位。</p>${report.holistic_notice?`<p class="pending-note">${esc(report.holistic_notice)}</p>`:''}</div>${rows.filter(r=>ranked.has(r.job.id)).map(rowHTML).join('')}${outside.length?`<details class="company-unranked" ${!choices.length?'open':''}><summary>${choices.length?'未参与排序的岗位':'范围内岗位'} ${outside.length} 个</summary>${outside.map(rowHTML).join('')}</details>`:''}${report.holistic?.questions?.length?`<div class="company-open-questions"><h3>还需确认</h3><ul>${report.holistic.questions.map(q=>`<li>${esc(q)}</li>`).join('')}</ul></div>`:''}</section><aside class="company-action-rail" aria-label="投递名额与已选岗位"><section><h2>本公司投递记录</h2>${workflow.applications?.length?`<ul class="company-applications">${workflow.applications.map(a=>{const row=rows.find(r=>r.job.id===a.job_id);return `<li><button class="text-btn" data-company-application="${esc(a.id)}">${esc(a.job?.title||row?.job.title||'范围外的投递记录')}</button><span>${esc(D.label('application',a.current_state))}${a.applied_at?' · 已实际投递':''}</span></li>`;}).join('')}</ul>`:'<p class="meta">还没有这家公司的投递记录。</p>'}<button class="btn btn-small" data-company-applications>管理投递与限投规则</button></section><section><h2>同批次投递名额</h2>${rules.length?rules.map(r=>`<article class="company-quota"><h3>${esc(r.name)}</h3><p><strong>${Number(r.remaining)}</strong> 个剩余名额 / 共 ${Number(r.limit)} 个</p><p class="meta">计划 ${Number(r.planned)} 个 · 已实际投递 ${Number(r.submitted)} 个 · 规则包含 ${r.job_ids.length} 个岗位</p>${r.conflict?'<p class="pending-note">记录超出当前规则，请核对。真实投递记录会保留。</p>':''}<p class="meta">只约束这条规则中选定的岗位；本次缩小比较范围不会释放名额。</p>${root.CampusCampaigns?.safeURL(r.rule_url)?`<a href="${esc(root.CampusCampaigns.safeURL(r.rule_url))}" target="_blank" rel="noopener noreferrer">查看规则来源</a>`:''}</article>`).join(''):'<p class="meta">尚未登记限投规则，不代表官网允许无限投递。</p>'}</section><p class="form-note">投递状态读取于 ${esc(D.date(workflow.as_of))}。</p></aside></div>`;
  }
  async function page(set,heading,{api,esc,D,navigate,active=()=>true,initialCompany='',initialIDs=[],initialEvaluation=false,capabilities=null}){
    const [cap,companies]=await Promise.all([capabilities||api('/api/profile/resume/capabilities'),api('/api/matching/company-catalog')]);if(!active())return;
    root.CampusModels.bindUser(cap.user_id);root.CampusMatching.bindUser(cap.user_id);if(root.CampusNavigation?.storeBrowse)root.CampusNavigation.storeBrowse(cap.user_id,{...root.CampusNavigation.readBrowse(cap.user_id),radarView:'company'});
    if(!companies.length){set(heading+'<div class="empty-state"><h2>先收集几份想投的岗位</h2><p>导入招聘来源后，可以在这里比较同公司岗位。</p><button class="btn" id="company-empty">去岗位雷达</button></div>');document.querySelector('#company-empty').onclick=()=>navigate('matching',{view:'list'});return;}
    const requestedIDs=[...new Set((Array.isArray(initialIDs)?initialIDs:[]).filter(id=>typeof id==='string'&&id))];
    let evaluationOpen=!!initialEvaluation;
    const remembered=initialCompany||lastCompany(cap.user_id)||root.CampusNavigation?.readBrowse?.(cap.user_id)?.company||'';
    let company=companies.some(c=>c.company===remembered)?remembered:companies[0].company,catalog=[],selected=new Set(),selectedScope=false,appliedIDs=[],data=null,busy=false,loadError=false,notice='',pending='',search='',catalogPage=1,revision=0,stateFilter='',fitFilter='',cityFilter='',sort='deep',onlySelected=false,showQuotaFull=false,quickCount=4,listScroll=0,detailRevision=0,writing=false;
    const q=s=>document.querySelector(s),identity=()=>root.CampusMatching.matchIdentity();
    function restorePicker(){const v=readState(cap.user_id,company);selected=new Set(v.selected);selectedScope=v.selectedScope;appliedIDs=v.scope;search=v.search;stateFilter=v.state;fitFilter=v.fit;cityFilter=v.city;sort=v.sort;onlySelected=v.onlySelected;showQuotaFull=v.showQuotaFull;quickCount=v.quickCount;catalogPage=v.page;listScroll=v.scroll;}
    function savePicker(readDOM=true){const list=readDOM?q('#company-picker-list'):null;if(list)listScroll=list.scrollTop||0;storeState(cap.user_id,company,{selected:[...selected],scope:appliedIDs,selectedScope,search,state:stateFilter,fit:fitFilter,city:cityFilter,sort,onlySelected,showQuotaFull,quickCount,page:catalogPage,scroll:listScroll});rememberCompany(cap.user_id,company);}
    function syncSelection(){const shared=root.CampusMatchingChat?.readSelection(cap.user_id)||new Set();for(const row of catalog)shared.delete(row.job.id);for(const id of selected)shared.add(id);root.CampusMatchingChat?.storeSelection(cap.user_id,shared);savePicker();}
    restorePicker();if(requestedIDs.length){selected=new Set(requestedIDs);selectedScope=true;appliedIDs=requestedIDs;}
    let annotationTop='',annotationReason='',annotationReviewed=false,evaluationPayload=null,annotationSaved=JSON.stringify({top:'',reason:'',reviewed:false});
    function scope(){return selectedScope?appliedIDs:[];}
    function annotationSnapshot(){const f=q('#company-eval-form');return JSON.stringify({top:f?f.elements.top.value:annotationTop,reason:f?f.elements.reason.value:annotationReason,reviewed:f?f.elements.reviewed.checked:annotationReviewed});}
    function captureAnnotation(){const form=q('#company-eval-form');if(form){annotationTop=form.elements.top.value;annotationReason=form.elements.reason.value;annotationReviewed=form.elements.reviewed.checked;}}
    function clearAnnotation(){annotationTop=annotationReason='';annotationReviewed=false;evaluationPayload=null;annotationSaved=JSON.stringify({top:'',reason:'',reviewed:false});}
    let chatReload=false;
    const chat=root.CampusCompanyChat?.create({api,esc,active,identity,notify,
      working:async value=>{captureAnnotation();busy=writing=value;if(!value&&chatReload){chatReload=false;await loadCompany(true);}else draw();},
      canImport:async doc=>{const ids=data?.comparison.holistic_job_ids||[],same=ids.length===doc.job_ids.length&&ids.every(id=>doc.job_ids.includes(id));return same||annotationSnapshot()===annotationSaved||await root.CampusNavigation.confirmDiscard();},
      onImported:async doc=>{evaluationPayload=null;selected=new Set(doc.job_ids);selectedScope=true;appliedIDs=[...selected];syncSelection();chatReload=true;}
    });
    function openEvaluation(){
      const section=q('#company-evaluation');if(!section)return;
      evaluationOpen=true;section.open=true;section.scrollIntoView({block:'start',behavior:'smooth'});q('#company-eval-form select')?.focus({preventScroll:true});
    }
    const pickerRows=()=>candidateRows(catalog,{search,state:stateFilter,fit:fitFilter,city:cityFilter,sort,onlySelected,showQuotaFull},selected);
    function picker(){
      const rows=pickerRows(),quotaHidden=candidateRows(catalog,{search,state:stateFilter,fit:fitFilter,city:cityFilter,onlySelected,showQuotaFull:true},selected).filter(r=>r.campaign?.hide_unsubmitted).length;catalogPage=Math.min(catalogPage,Math.max(1,Math.ceil(rows.length/30)));const shown=rows.slice((catalogPage-1)*30,catalogPage*30),visibleIDs=new Set(rows.map(r=>r.job.id)),hidden=[...selected].filter(id=>!visibleIDs.has(id)).length,changed=selectedScope&&([...selected].some(id=>!appliedIDs.includes(id))||selected.size!==appliedIDs.length);
      const scoreHTML=r=>r.state!=='ANALYZED'?`<span class="company-candidate-state">${esc(states[r.state]||'待分析')}</span>`:r.fit&&sort!=='local'&&sort!=='technical'?`<strong class="company-fit company-fit-${esc(r.fit.toLowerCase())}">${esc(fitLabels[r.fit]||'待核对')}</strong><small>深度分析</small>`:candidateScore(r,sort)==null?'<span class="company-candidate-state">暂无数值评分</span>':`<strong>${candidateScore(r,sort).toFixed(1)}</strong><small>${sort==='local'?'初筛参考':sort==='technical'||!Number.isFinite(r.priority?.score)?'旧版技术参考':'旧版投递参考'}</small>`;
      return `<div class="company-picker-meta"><span role="status" aria-live="polite" aria-atomic="true">${rows.length} 个符合筛选${quotaHidden&&!showQuotaFull&&!onlySelected?' · 已收起 '+quotaHidden+' 个限投已满岗位':''}${hidden?' · '+hidden+' 个已选岗位在筛选外':''}</span><button id="company-clear-filters" class="text-btn" ${!search&&!stateFilter&&!fitFilter&&!cityFilter&&!onlySelected&&!showQuotaFull?'disabled':''}>清除筛选</button></div><div id="company-picker-list" class="company-picker-list" data-scroll-region aria-label="候选岗位列表">${shown.map(r=>`<article class="company-candidate ${selected.has(r.job.id)?'is-selected':''}"><label class="company-candidate-check"><input type="checkbox" data-company-select="${esc(r.job.id)}" aria-label="${esc('选择 '+r.job.title)}" ${selected.has(r.job.id)?'checked':''} ${busy?'disabled':''}></label><div class="company-candidate-main"><button class="text-btn" data-company-detail="${esc(r.job.id)}">${esc(r.job.title)}</button><p>${esc(r.job.locations.join(' / ')||'地点待核对')}${r.application?' · '+esc(D.label('application',r.application.current_state)):''}${r.excluded_reason?' · '+esc(r.excluded_reason):''}${r.campaign?.hide_unsubmitted?' · 本批已投满 '+r.campaign.submitted+'/'+r.campaign.limit:''}</p></div><div class="company-candidate-score">${scoreHTML(r)}</div></article>`).join('')||'<p class="meta">没有找到岗位，换个筛选条件试试。已选岗位会保留。</p>'}</div><div class="company-picker-pagination"><button class="btn btn-small" id="company-picker-prev" ${catalogPage===1||busy?'disabled':''}>上一页</button><span>${catalogPage} / ${Math.max(1,Math.ceil(rows.length/30))} 页</span><button class="btn btn-small" id="company-picker-next" ${catalogPage>=Math.ceil(rows.length/30)||busy?'disabled':''}>下一页</button></div><div class="company-selection-tray ${selected.size&&rows.length>6?'is-sticky':''}"><div><strong>已选 ${selected.size} / 16</strong><span>${changed?'候选范围有变化，点击查看后更新报告':'勾选会自动记住，切换视图也保留'}</span></div><div class="actions"><label>快速勾选<select id="company-quick-count" aria-label="快速勾选岗位数量">${[4,8,16].map(n=>`<option value="${n}" ${quickCount===n?'selected':''}>前 ${n} 个</option>`).join('')}</select></label><button class="btn btn-small" id="company-select-top" ${busy?'disabled':''}>按当前排序补选</button><button class="text-btn" id="company-clear-selection" ${busy||!selected.size?'disabled':''}>清空勾选</button><button class="btn btn-primary" id="company-apply-scope" ${busy||!selected.size?'disabled':''}>查看所选岗位</button></div></div>${selected.size?`<details id="company-selected-list" data-remember class="company-selected-list"><summary>查看已选清单</summary>${[...selected].map(id=>`<div><span>${esc(catalog.find(r=>r.job.id===id)?.job.title||'岗位已不可用')}</span><button class="text-btn" data-company-remove="${esc(id)}" ${busy?'disabled':''} aria-label="${esc('移除 '+(catalog.find(r=>r.job.id===id)?.job.title||'岗位'))}">移除</button></div>`).join('')}</details>`:''}`;
    }
    function updatePicker(resetScroll=false){
      savePicker();if(resetScroll)listScroll=0;
      const focus=document.activeElement,focusedJob=focus?.dataset?.companySelect,focusedRemoval=focus?.dataset?.companyRemove,listOpen=q('#company-selected-list')?.open;
      const focusedID=['company-picker-prev','company-picker-next','company-select-top','company-clear-selection','company-clear-filters','company-quick-count'].includes(focus?.id)?focus.id:'';
      q('#company-picker-content').innerHTML=picker();bindPicker();if(listOpen&&q('#company-selected-list'))q('#company-selected-list').open=true;
      if(focusedJob||focusedID||focusedRemoval){const target=focusedID?q('#'+focusedID):focusedRemoval?document.querySelectorAll('[data-company-remove]')[0]:[...document.querySelectorAll('[data-company-select]')].find(el=>el.dataset.companySelect===focusedJob);(target&&!target.disabled?target:q('#company-picker-search'))?.focus({preventScroll:true});}
      const analyze=q('#company-analyze');if(analyze)analyze.disabled=busy||loadError||(selected.size?selected.size>16:!data?.comparison.holistic_job_ids?.length||!!data?.comparison.holistic_notice);savePicker();}
    async function applyScope(){if(busy)return false;captureAnnotation();if((annotationSnapshot()!==annotationSaved||chat?.isDirty())&&!await root.CampusNavigation.confirmDiscard())return false;if(!selected.size){notify('请先勾选至少一个候选岗位。');return false;}selectedScope=true;appliedIDs=[...selected];savePicker();data=null;clearAnnotation();await loadWorkspace();q('#company-workspace')?.scrollIntoView({block:'start',behavior:'smooth'});return !loadError&&active();}
    function bindPicker(){
      const list=q('#company-picker-list');if(list){list.scrollTop=listScroll;list.onscroll=()=>{listScroll=list.scrollTop;savePicker();};}
      for(const el of document.querySelectorAll('[data-company-select]'))el.onchange=()=>{if(busy)return;if(el.checked&&selected.size>=16){el.checked=false;notify('每次整体比较最多选择 16 个岗位。');return;}el.checked?selected.add(el.dataset.companySelect):selected.delete(el.dataset.companySelect);syncSelection();updatePicker();};
      for(const b of document.querySelectorAll('[data-company-remove]'))b.onclick=()=>{selected.delete(b.dataset.companyRemove);syncSelection();updatePicker();};
      for(const b of document.querySelectorAll('#company-picker-content [data-company-detail]'))b.onclick=()=>showDetail(b.dataset.companyDetail);
      q('#company-picker-prev').onclick=()=>{catalogPage--;updatePicker(true);};q('#company-picker-next').onclick=()=>{catalogPage++;updatePicker(true);};
      q('#company-apply-scope').onclick=applyScope;
      q('#company-quick-count').onchange=e=>{quickCount=Number(e.target.value)||4;savePicker();};
      q('#company-select-top').onclick=()=>{const count=Number(q('#company-quick-count').value)||4;quickCount=count;const available=pickerRows().filter(r=>!r.excluded_reason&&r.job.current_status!=='CLOSED'&&!selected.has(r.job.id));const added=available.slice(0,Math.min(count,Math.max(0,16-selected.size)));for(const row of added)selected.add(row.job.id);syncSelection();updatePicker();notify(added.length?'已按当前排序补选 '+added.length+' 个岗位，原有勾选已保留。':'没有可补选的岗位，或已达到 16 个上限。');};
      q('#company-clear-selection').onclick=()=>{selected.clear();syncSelection();updatePicker();};
      q('#company-clear-filters').onclick=()=>{search=stateFilter=fitFilter=cityFilter='';onlySelected=showQuotaFull=false;catalogPage=1;listScroll=0;captureAnnotation();draw();savePicker();};
    }
    function notify(message){notice=message;const el=q('#company-notice');if(el)el.textContent=message;}
    async function showDetail(id,view='overview'){
      const row=catalog.find(r=>r.job.id===id);if(!row||busy||loadError||!root.CampusUI)return;
      savePicker();const U=root.CampusUI,rev=++detailRevision,employer=company;
      const header=`<div class="drawer-top"><div class="drawer-topline"><span>${esc(company)} · 公司投递决策</span><button type="button" class="icon-btn" data-dialog-close aria-label="关闭岗位详情">${U.icon('close')}</button></div><h2 id="drawer-title">${esc(row.job.title)}</h2><p class="drawer-subtitle">${esc(row.job.locations.join(' / ')||'地点待核对')}</p></div>`;
      const opened=U.drawer(header+'<div class="drawer-body"><p>正在读取已保存的分析…</p></div>');
      const valid=()=>active()&&company===employer&&rev===detailRevision&&U.drawerCurrent(opened.revision);
      try{
        const [result,record,plan]=await Promise.all([api('/api/matching/results/'+encodeURIComponent(id),'POST',identity()),api('/api/jobs/'+encodeURIComponent(id)),view==='preparation'?api('/api/matching/preparation/'+encodeURIComponent(id),'POST',identity()):null]);
        if(!valid())return;
        const link=root.CampusApplications?.officialLink(record.official_url),source=(record.observations||[]).find(o=>o.text)?.text||'暂无可用岗位原文。',done=plan?root.CampusDecision.readProgress(cap.user_id,plan):null;
        opened.element.innerHTML=header+`<div class="drawer-body">${link?`<a class="btn btn-small" href="${esc(link)}" target="_blank" rel="noopener noreferrer">查看官网岗位</a>`:''}<div class="actions company-detail-actions"><button class="btn btn-small" data-company-detail-view="overview" ${view==='overview'?'disabled':''}>匹配结论</button><button class="btn btn-small" data-company-detail-view="preparation" ${view==='preparation'?'disabled':''}>准备清单</button></div>${plan?root.CampusDecision.renderPreparation(plan,{esc,D},done):root.CampusMatching.renderResult(result,{esc,D})}<details><summary>查看完整岗位原文</summary><div class="drawer-original">${esc(source)}</div></details></div>`;
        for(const b of opened.element.querySelectorAll('[data-company-detail-view]'))b.onclick=()=>showDetail(id,b.dataset.companyDetailView);
        for(const b of opened.element.querySelectorAll('[data-supplement]'))b.onclick=()=>navigate('profile',{evidence:{jobID:id,requirementID:b.dataset.supplement,identity:identity()}});
        for(const b of opened.element.querySelectorAll('[data-prep-analyze]'))b.onclick=()=>navigate('matching',{view:'list',jobID:id,analyze:true,returnCompany:company});
        if(plan)for(const input of opened.element.querySelectorAll('[data-prep-task]'))input.onchange=()=>{if(input.checked)done.add(input.dataset.prepTask);else done.delete(input.dataset.prepTask);input.closest('.prep-task').classList.toggle('is-done',input.checked);opened.element.querySelector('[data-prep-count]').textContent=`已完成 ${done.size} / ${plan.tasks.length}`;if(!root.CampusDecision.saveProgress(cap.user_id,plan,done))opened.element.querySelector('[data-prep-storage]').textContent='浏览器未允许保存，勾选仅在本次打开时有效。';};
      }catch(error){if(valid()){opened.element.innerHTML=header+`<div class="drawer-body"><p class="pending-note">${esc(error.message)}</p><button class="btn" data-company-detail-retry>重新读取</button></div>`;opened.element.querySelector('[data-company-detail-retry]').onclick=()=>showDetail(id,view);}}
    }
    function draw(){
      if(!active())return;
      const report=data?.comparison,savedUI=root.CampusUI?.capture();
      if(!set(`${heading}<nav class="radar-view-tabs" aria-label="岗位雷达视图"><button id="company-list-view" aria-pressed="false">岗位列表</button><button aria-pressed="true" disabled>公司投递决策</button></nav><div class="company-decision-toolbar"><label>选择公司<select id="company-switch" ${busy?'disabled':''}>${companies.map(c=>`<option value="${esc(c.company)}" ${company===c.company?'selected':''}>${esc(c.company)}（${c.total}）</option>`).join('')}</select></label><div class="actions"><button class="btn" id="company-refresh" ${busy?'disabled':''}>${busy?'正在读取…':'刷新当前范围'}</button><button class="btn btn-primary" id="company-analyze" ${busy||loadError||(selected.size?selected.size>16:!report?.holistic_job_ids?.length||report.holistic_notice)?'disabled':''}>${report?.holistic?'核对当前比较资料':'核对资料并生成比较'}</button><button class="btn" id="company-open-chat" ${busy?'disabled':''}>用 GPT 聊天比较</button><button class="btn" id="company-open-evaluation" type="button" aria-controls="company-evaluation" ${busy||loadError||!report?.holistic_input_key||report.holistic_notice?'disabled':''}>导出评测 JSON</button></div></div><section class="company-picker" aria-busy="${busy}" aria-labelledby="company-picker-heading"><div class="company-picker-heading"><div><h2 id="company-picker-heading">候选岗位</h2></div><button class="text-btn" id="company-all-scope" ${busy||catalog.length>200?'disabled':''}>查看全部本地岗位</button></div><div class="company-picker-filters"><label class="company-picker-search">查找岗位<input id="company-picker-search" type="search" value="${esc(search)}" placeholder="例如：服务端、Agent、上海"></label><label>分析状态<select id="company-picker-state">${[['','全部状态'],['ANALYZED','已深度分析'],['BASIC','待分析'],['STALE','分析待更新']].map(([v,n])=>`<option value="${v}" ${stateFilter===v?'selected':''}>${n}</option>`).join('')}</select></label><label>整体适配<select id="company-picker-fit">${[['','全部适配'],['RELEVANT','优先或相关'],...Object.entries(fitLabels)].map(([v,n])=>`<option value="${v}" ${fitFilter===v?'selected':''}>${n}</option>`).join('')}</select></label><label>城市<select id="company-picker-city"><option value="">全部城市</option>${[...new Set(catalog.flatMap(r=>r.job.locations))].sort((a,b)=>a.localeCompare(b,'zh')).map(v=>`<option value="${esc(v)}" ${cityFilter===v?'selected':''}>${esc(v)}</option>`).join('')}</select></label><label>排序<select id="company-picker-sort">${[['deep','深度分析优先'],['technical','旧版技术分从高到低'],['local','初筛参考分从高到低'],['title','岗位名称']].map(([v,n])=>`<option value="${v}" ${sort===v?'selected':''}>${n}</option>`).join('')}</select></label></div><div class="company-picker-options"><label class="check"><input id="company-picker-quota" type="checkbox" ${showQuotaFull?'checked':''}>显示限投已满岗位</label><label class="check"><input id="company-picker-selected" type="checkbox" ${onlySelected?'checked':''}>只看已选</label></div><details id="company-sort-guide" data-remember class="company-sort-guide"><summary>排序怎么看</summary><p>优先、相关等是整体分析结论；历史数值分单独排序。资料或岗位变化后，旧分析会标为待更新，不参与当前排名。</p></details><div id="company-picker-content">${picker()}</div>${catalog.length>200?'<p class="meta">本地岗位超过 200 个，请先筛选并勾选。候选列表仍可查看全部岗位。</p>':''}</section><p id="company-notice" role="status" class="company-notice ${loadError?'is-error':''}">${esc(notice)}</p>${chat?.render({company,jobIDs:report?.holistic_job_ids||[],inputKey:report?.holistic_input_key,disabled:busy,exportDisabled:loadError||!!report?.holistic_notice})||''}<div id="company-workspace" aria-busy="${busy}">${data?renderWorkspace(data,{esc,D},pending):'<div class="empty-state"><h2>选出一组候选岗位</h2><p>勾选岗位后，查看或生成同公司比较。</p></div>'}</div>${data?`<details id="company-evaluation" class="company-evaluation" ${evaluationOpen||annotationTop||annotationReason||evaluationPayload?'open':''}><summary>导出评测 JSON · 标注这组排序</summary><p>标注你认可的首选和理由，预览后下载 JSON。</p><p class="meta">${report.holistic?'当前范围已有比较，导出文件会包含本次排序与关键依据。':'当前范围尚无有效比较，文件只包含材料和人工标注；需要分析结果时，请先生成这组比较。'}</p><form id="company-eval-form"><label>我认可的首选<select name="top" required><option value="">请自行选择，不默认采用模型首选</option>${(report.jobs||[]).filter(j=>(report.holistic_job_ids||[]).includes(j.job.id)).map(j=>`<option value="${esc(j.job.id)}" ${annotationTop===j.job.id?'selected':''}>${esc(j.job.title)}</option>`).join('')}</select></label><label>选择理由<textarea name="reason" required maxlength="2000" placeholder="例如：主要工作与我的 Go 服务项目更相近，算法研究要求较少。">${esc(annotationReason)}</textarea></label><label class="check"><input type="checkbox" name="reviewed" required ${annotationReviewed?'checked':''}>我已阅读这组岗位与求职材料，认可上述参考结论</label><p class="form-note">导出前请检查完整资料中的敏感文字。</p><button class="btn" ${busy||loadError||!report.holistic_input_key||report.holistic_notice?'disabled':''}>预览导出 JSON</button></form><div id="company-eval-preview">${evaluationPayload?`<h3>核对实际导出的资料</h3><p>包含当前脱敏求职材料、完整岗位原文、人工标注与可复用的比较结果。请检查姓名等敏感文字，再决定下载。</p><details open><summary>查看完整案例文字</summary><pre class="company-eval-document">${esc(JSON.stringify(evaluationPayload,null,2))}</pre></details><button class="btn btn-primary" id="company-eval-download" ${busy||loadError?'disabled':''}>确认下载 JSON</button>`:''}</div></details>`:''}`))return;
      q('#company-list-view').onclick=()=>{syncSelection();navigate('matching',{view:'list'});};
      q('#company-switch').onchange=async e=>{const next=e.target.value;if(!await root.CampusNavigation.leave()){e.target.value=company;return;}savePicker();company=next;restorePicker();data=null;pending='';notice='';clearAnnotation();await loadCompany();};
      q('#company-picker-search').oninput=e=>{search=e.target.value;catalogPage=1;updatePicker(true);};
      for(const [id,change] of [['company-picker-state',v=>stateFilter=v],['company-picker-fit',v=>fitFilter=v],['company-picker-city',v=>cityFilter=v],['company-picker-sort',v=>sort=v],['company-picker-selected',(_v,el)=>onlySelected=el.checked],['company-picker-quota',(_v,el)=>showQuotaFull=el.checked]])q('#'+id).onchange=e=>{change(e.target.value,e.target);catalogPage=1;updatePicker(true);};
      bindPicker();
      q('#company-refresh').onclick=()=>loadCompany(true);
      q('#company-all-scope').onclick=async()=>{if(busy)return;captureAnnotation();if((annotationSnapshot()!==annotationSaved||chat?.isDirty())&&!await root.CampusNavigation.confirmDiscard())return;selectedScope=false;appliedIDs=[];savePicker();data=null;clearAnnotation();await loadWorkspace();};
      q('#company-open-evaluation').onclick=openEvaluation;
      if(q('#company-open-chat'))q('#company-open-chat').onclick=async()=>{if(busy)return;if(selected.size&&(!selectedScope||selected.size!==appliedIDs.length||[...selected].some(id=>!appliedIDs.includes(id)))&&!await applyScope())return;chat?.open();};
      chat?.bind();
      if(q('#company-evaluation'))q('#company-evaluation').ontoggle=e=>{evaluationOpen=e.target.open;};
      q('#company-analyze').onclick=async()=>{if(busy||loadError)return;if(selected.size&&(!selectedScope||selected.size!==appliedIDs.length||[...selected].some(id=>!appliedIDs.includes(id)))&&!await applyScope())return;const current=data?.comparison;if(!current?.holistic_job_ids?.length||current.holistic_notice||loadError||busy)return;savePicker();navigate('matching',{view:'list',returnCompany:company,comparison:{company,jobIDs:current.holistic_job_ids,review:true}});};
      for(const b of document.querySelectorAll('[data-company-detail]'))b.onclick=()=>showDetail(b.dataset.companyDetail);
      for(const b of document.querySelectorAll('[data-company-prepare]'))b.onclick=()=>showDetail(b.dataset.companyPrepare,'preparation');
      for(const b of document.querySelectorAll('[data-company-application]'))b.onclick=()=>navigate('applications',{applicationID:b.dataset.companyApplication});
      for(const b of document.querySelectorAll('[data-company-applications]'))b.onclick=()=>navigate('applications');
      for(const b of document.querySelectorAll('[data-company-plan]'))b.onclick=()=>{const context=data.workflow.jobs.find(c=>c.job_id===b.dataset.companyPlan);if(context?.application){navigate('applications',{applicationID:context.application.id});return;}captureAnnotation();pending=b.dataset.companyPlan;draw();};
      for(const b of document.querySelectorAll('[data-company-plan-cancel]'))b.onclick=()=>{captureAnnotation();pending='';draw();};
      for(const b of document.querySelectorAll('[data-company-plan-confirm]'))b.onclick=async()=>{
        if(busy)return;captureAnnotation();const id=b.dataset.companyPlanConfirm;busy=true;writing=true;draw();
        try{await api('/api/applications','POST',{job_id:id});pending='';notice='已加入投递计划。';}
        catch(error){notice=error.message;}
        finally{busy=false;writing=false;if(active())await loadWorkspace(true);}
      };
      if(q('#company-eval-form'))q('#company-eval-form').onsubmit=async e=>{
        e.preventDefault();if(busy||!e.target.reportValidity())return;captureAnnotation();busy=true;draw();
        try{
          const payload=await api('/api/matching/evaluation/export','POST',{...identity(),company,job_ids:report.holistic_job_ids,expected_scope_key:report.holistic_input_key,reference:{acceptable_top_job_ids:[annotationTop],reason:annotationReason,reviewed:annotationReviewed}});
          if(!active())return;
          evaluationPayload=payload;evaluationOpen=true;
        }catch(error){if(active())notify(error.message);}
        finally{busy=false;if(active()){draw();if(evaluationPayload)q('#company-eval-preview')?.scrollIntoView({block:'start',behavior:'smooth'});}}
      };
      root.CampusNavigation.register({active,checkpoint:savePicker,dirty:()=>writing||!!chat?.isDirty()||annotationSnapshot()!==annotationSaved});
      if(q('#company-eval-download'))q('#company-eval-download').onclick=()=>{const payload=evaluationPayload,blob=new Blob([JSON.stringify(payload,null,2)],{type:'application/json;charset=utf-8'}),url=URL.createObjectURL(blob),a=document.createElement('a');a.href=url;a.download='CampusTrace-匹配评测-'+payload.cases[0].id.slice(0,12)+'.json';document.body.append(a);a.click();a.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);const ref=payload.cases[0].reference;annotationSaved=JSON.stringify({top:ref.acceptable_top_job_ids[0],reason:ref.reason,reviewed:ref.reviewed});notify('评测 JSON 已下载。');};
      if(q('#company-eval-form'))q('#company-eval-form').oninput=()=>{evaluationPayload=null;const preview=q('#company-eval-preview');if(preview)preview.replaceChildren();};
      root.CampusUI?.restore(savedUI);
      if(busy||loadError){for(const b of document.querySelectorAll('#company-workspace button'))b.disabled=true;for(const el of q('#company-eval-form')?.elements||[])el.disabled=true;}
    }
    async function loadWorkspace(keepNotice=false){
      if(busy||!active())return;if(!selectedScope&&catalog.length>200){notify('请先勾选候选岗位，再查看所选范围。');return;}captureAnnotation();const rev=++revision;busy=true;pending='';if(!keepNotice)notice='';draw();
      try{const result=await api('/api/matching/company-workspace','POST',{...identity(),company,job_ids:scope()});if(!active()||rev!==revision)return;if(data?.comparison.holistic_input_key!==result.comparison.holistic_input_key)clearAnnotation();data=result;loadError=false;}
      catch(error){if(active()&&rev===revision){loadError=true;notice=error.message+(data?' 当前显示上次读取的报告，标注已保留；请刷新后再操作。':'');}}
      finally{if(active()&&rev===revision){busy=false;draw();}}
    }
    async function loadCompany(keepNotice=false){
      const rev=++revision;busy=true;captureAnnotation();savePicker(false);draw();
      try{
        const rows=await api('/api/matching/company-candidates','POST',{...identity(),company});if(!active()||rev!==revision)return;
        catalog=rows;
        const known=new Set(rows.map(r=>r.job.id)),missing=appliedIDs.filter(id=>!known.has(id));
        if(!requestedIDs.length||company!==initialCompany){const shared=root.CampusMatchingChat?.readSelection(cap.user_id);if(shared)selected=new Set([...shared].filter(id=>known.has(id)));}
        selected=new Set([...selected].filter(id=>known.has(id)));syncSelection();
        if(missing.length){loadError=true;notice='本次比较中有岗位已不在当前公司列表里，请重新选择候选岗位后导出。';return;}
        loadError=false;
      }catch(error){if(active()&&rev===revision){loadError=true;notice=error.message+(catalog.length?' 当前列表保留，分析状态可能已变化，请刷新后再操作。':'');}}
      finally{if(active()&&rev===revision){busy=false;draw();}}
      if(active()&&!loadError&&catalog.length&&(selectedScope||catalog.length<=16))await loadWorkspace(keepNotice);
    }
    await loadCompany();
    if(active()&&initialEvaluation)openEvaluation();
  }
  const api={page,renderWorkspace,orderedRows,planningState,candidateRows,candidateScore,readState,storeState};if(typeof module==='object'&&module.exports)module.exports=api;root.CampusCompanyDecision=api;return api;
})(typeof window==='undefined'?globalThis:window);
