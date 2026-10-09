'use strict';
const D = CampusDisplay;
CampusUI.init();
const $ = selector => document.querySelector(selector);
const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
let token = sessionStorage.getItem('campustrace-token') || '';
let pageVersion = 0;
class UserError extends Error {}
function empty(message='暂无记录。') { return `<p class="empty">${esc(message)}</p>`; }
function pill(value,group='job') { return `<span class="pill ${Object.hasOwn(D.enums[group]||{},value)?esc(value):''}">${esc(D.label(group,value))}</span>`; }
function translated(value,key='',context={}) {
  if (Array.isArray(value)) return value.length?`<ul>${value.map(v=>`<li>${translated(v,key,context)}</li>`).join('')}</ul>`:empty('暂无相关记录。');
  if (value && typeof value==='object') {
    if (key==='breakdown') return `<dl class="records">${Object.entries(value).map(([k,v])=>`<div><dt>${esc(D.label('ranking',k))}</dt><dd>${Number(v).toFixed(1)} 分</dd></div>`).join('')}</dl>`;
    return `<dl class="records">${Object.entries(value).filter(([key])=>key!=='embedding').map(([key,v])=>`<div><dt>${esc(D.field(key))}</dt><dd>${translated(v,key,value)}</dd></div>`).join('')}</dl>`;
  }
  // Source evidence and documents remain literal, and are clearly labelled as such.
  if (key==='excerpt'||key==='text') return `<div class="original"><small>来源原文，未作翻译</small><pre>${esc(value||'暂无原文')}</pre></div>`;
  return esc(D.scalar(key,value,context));
}
function table(rows,columns,message='暂无记录。') {
  if (!rows?.length) return empty(message);
  return `<div class="table-wrap"><table><thead><tr>${columns.map(c=>`<th scope="col">${esc(D.field(c))}</th>`).join('')}</tr></thead><tbody>${rows.map(row=>`<tr>${columns.map(key=>`<td>${translated(row[key],key,row)}</td>`).join('')}</tr>`).join('')}</tbody></table></div>`;
}
function options(group,selected,blank=false) {
  return (blank?'<option value="">请选择</option>':'')+Object.entries(D.enums[group]).map(([value,label])=>`<option value="${esc(value)}" ${value===selected?'selected':''}>${esc(label)}</option>`).join('');
}
async function api(path,method='GET',body) {
  let response;
  try { response=await fetch(path,{method,headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)}); }
  catch { throw new UserError('暂时无法连接服务，请检查网络后重试。'); }
  if (!response.ok) { const details=await response.json().catch(()=>({}));const error=new UserError(D.errorCode(details.code,response.status,path));error.status=response.status;error.code=details.code;if(path.includes('/api/matching/')){const jobIndex=details.diagnostic?.job_index;if(Number.isInteger(jobIndex)&&jobIndex>0&&jobIndex<=100)error.jobIndex=jobIndex;const stage={PREPARE:'准备资料',EXTRACT:'提取岗位要求',COMPARE:'匹配个人资料',SAVE:'保存结果'}[details.diagnostic?.stage];const status=details.diagnostic?.provider_status;const id=details.request_id||response.headers.get('X-Request-ID');if(stage)error.message+=' 失败阶段：'+stage+'。';error.message+=D.matchingDiagnostic(details.diagnostic);if(Number.isInteger(status)&&status>=100&&status<=599)error.message+=' 模型服务响应：HTTP '+status+'。';if(/^[a-f0-9]{32}$/.test(id||''))error.message+=' 请求编号：'+id+'。';}if(path==='/api/profile/resume/draft'){const reason=D.resumeDiagnostic(details.diagnostic);if(reason)error.message+=' 具体原因：'+reason+'。';const id=details.request_id||response.headers.get('X-Request-ID');if(/^[a-f0-9]{32}$/.test(id||''))error.message+=' 请求编号：'+id+'。';}throw error; }
  try { return await response.json(); } catch { throw new UserError('服务返回的内容暂时无法显示，请稍后重试。'); }
}
function fail(error) { $('#notice').textContent=error instanceof UserError?error.message:'暂时无法完成操作，请检查填写内容或稍后重试。'; }
function formAction(selector,action) {
  $(selector).onsubmit=async event=>{
    event.preventDefault(); $('#notice').textContent='';const form=event.target;
    const invalid=[...form.elements].find(input=>input.willValidate&&!input.validity.valid);
    if (invalid) { $('#notice').textContent=invalid.type==='email'?'请填写有效的邮箱地址。':'请补全必填项，并检查数字范围和填写格式。';invalid.focus();return; }
    const buttons=[...form.querySelectorAll('button[type="submit"], button:not([type])')];buttons.forEach(b=>b.disabled=true);
    try { await action(new FormData(form),form); } catch(error) { fail(error); } finally { buttons.forEach(b=>b.disabled=false); }
  };
}
function show() { $('#auth').hidden=!!token;$('#workspace').hidden=!token;document.body.classList.toggle('signed-in',!!token);document.title='CampusTrace · 校招求职工作台';if(token)page('matching').catch(fail); }
formAction('#login',async data=>{
  token=(await api('/auth/login','POST',Object.fromEntries(data))).token;
  sessionStorage.setItem('campustrace-token',token);show();
});
$('#register').onclick=async()=>{
  const form=$('#login'),data=Object.fromEntries(new FormData(form));
  if (!data.email||!form.elements.email.validity.valid) {fail(new UserError('请填写有效的邮箱地址。'));return;}
  const size=new TextEncoder().encode(data.password).length;
  if (size<10||size>72) {fail(new UserError('密码长度需为 10—72 字节；汉字通常占多个字节，建议使用字母、数字和符号组合。'));return;}
  try {await api('/auth/register','POST',data);$('#notice').textContent='账号已创建，请使用刚填写的邮箱和密码登录。';}catch(error){fail(error);}
};
$('#logout').onclick=async()=>{if(!await CampusNavigation.leave())return;CampusUI.closeAll();pageVersion++;token='';sessionStorage.removeItem('campustrace-token');globalThis.CampusModels?.lock();globalThis.CampusMatching?.lock();globalThis.CampusKnowledge?.lock();globalThis.CampusDrafts?.lock();$('#content').replaceChildren();$('#notice').textContent='';show();};
for (const b of document.querySelectorAll('[data-page]')) b.onclick=()=>page(b.dataset.page).catch(fail);
const pageTitles={radar:'我的校招雷达',watches:'关注源',source_jobs:'来源岗位',notifications:'通知收件箱',preferences:'稍后看与忽略',closing:'截止雷达',changes:'最近变化',jobs:'校招岗位',matching:'岗位雷达',company_decision:'公司投递决策',applications:'投递进展',interviews:'面试与复盘',weak_topics:'待加强知识点',project_facts:'项目事实',practice:'Go 练习',agent:'求职问答',knowledge:'学习资料',profile:'求职资料',models:'模型设置',ingest:'录入岗位'};
function input(name,label,value='',type='text',extra='') {return `<label>${esc(label)}<input name="${esc(name)}" type="${type}" value="${esc(value)}" ${extra}></label>`;}
function area(name,label,value='',extra='') {return `<label>${esc(label)}<textarea name="${esc(name)}" ${extra}>${esc(value)}</textarea></label>`;}
function displayQuery(query) {
  const samples={'雪松':'Synthetic Cedar','港湾':'Synthetic Harbor','枫叶':'Synthetic Maple','青松':'Synthetic Pine','后端':'backend','实习':'intern'};
  return samples[query.trim()]||query;
}
async function page(name,query='') {
  if(!await CampusNavigation.leave())return;
  if(name==='project_facts')name='profile';
  if(name==='jobs')name='matching';
  if(name==='company_decision'){name='matching';query={...(typeof query==='object'?query:{}),view:'company'};}
  CampusUI.closeAll();
  const navName=name==='source_jobs'?'watches':['closing','changes'].includes(name)?'radar':name;
  for(const b of document.querySelectorAll('nav [data-page]')){if(b.dataset.page===navName)b.setAttribute('aria-current','page');else b.removeAttribute('aria-current');}
  $('#crumb').textContent=pageTitles[name]||'求职记录';
  document.querySelector('.sidebar').classList.remove('menu-open');
  document.querySelector('#mobile-menu')?.setAttribute('aria-expanded','false');
  const version=++pageVersion;$('#notice').textContent='';const box=$('#content');box.innerHTML=CampusUI.heading(pageTitles[name]||'求职记录')+CampusUI.loading();
  document.title=`${pageTitles[name]||'求职记录'} · CampusTrace`;
  let entered=false;
  const set=html=>{if(version!==pageVersion)return false;box.innerHTML=html;if(!entered){entered=true;CampusUI.reveal(box);}return true;};
  const heading=CampusUI.heading(pageTitles[name]);
  const route=name==='matching'&&query?.view==='company'?'company':name==='profile'&&query?.evidence?'evidence':['radar','watches','source_jobs','notifications','preferences','closing','changes'].includes(name)?'radar':name;
  try{await CampusAssets.ensure(route);}catch(error){if(version===pageVersion){set(heading+'<p class="empty">页面组件暂时无法读取。</p><button id="page-load-retry" class="btn">重新读取页面</button>');$('#page-load-retry').onclick=()=>page(name,query).catch(fail);}return;}
  if(version!==pageVersion)return;
  if(name==='knowledge'){await CampusKnowledge.page(set,heading,{api,esc,formAction,UserError,navigate:page,initialDocument:query?.documentID||'',active:()=>version===pageVersion});return;}
  if(name==='practice'){await CampusPractice.page(set,heading,{api,esc,formAction,UserError,active:()=>version===pageVersion});return;}
  if (name==='models') {await CampusModels.page(set,heading,{api,esc,formAction,UserError,isCurrent:()=>version===pageVersion});return;}
  if(name==='matching'&&query?.view==='company'){await CampusCompanyDecision.page(set,heading,{api,esc,D,navigate:page,active:()=>version===pageVersion,initialCompany:query?.company||'',initialIDs:query?.jobIDs||[],initialEvaluation:!!query?.evaluation});return;}
  if (name==='matching') {await CampusMatching.page(set,heading,{api,esc,D,UserError,navigate:page,openRecord:detail,initialQuery:typeof query==='string'?query:'',initialJob:query?.jobID||'',initialView:query?.view||'overview',initialAnalyze:!!query?.analyze,initialTask:query?.taskID||'',initialComparison:query?.comparison||null,initialWorkflow:query?.workflow||'',returnCompany:query?.returnCompany||'',active:()=>version===pageVersion});return;}
  if(['radar','watches','source_jobs','notifications','preferences','closing','changes'].includes(name)){await radarPage(name,set,box,query,{active:()=>version===pageVersion,reviewInterview});return;}
  if (name==='agent') {
    const capabilities=await api('/api/profile/resume/capabilities');
    if(version!==pageVersion)return;
    CampusModels.bindUser(capabilities.user_id);
    CampusKnowledge.bindUser(capabilities.user_id);
    CampusMatching.bindUser(capabilities.user_id);
    const composer=`<section class="agent-panel agent-composer"><div class="agent-section-head"><h2>接下来，先做哪件事？</h2><button id="agent-model-settings" type="button" class="text-btn">选择模型</button></div><p class="agent-intro">比较岗位、查投递记录，或从面试复盘里找出下一步。</p><form id="ask" novalidate><label class="visually-hidden" for="agent-question">你的问题</label><textarea id="agent-question" name="message" rows="4" required maxlength="4000" placeholder="例如：小红书这几个后端岗位，我应该优先投哪一个？">${esc(query?.message||'')}</textarea><div class="agent-question-footer"><span class="meta">${esc(CampusModels.available(capabilities)?CampusModels.label(capabilities):'离线演示模型')}</span><button type="submit" class="btn btn-primary">查询求职记录</button></div></form><div class="agent-examples" aria-label="试着这样问">${['今天有什么值得处理？','我最近的深度分析进度如何？','我投过哪些岗位？','未来三天哪些岗位截止？'].map(q=>`<button type="button" data-example="${esc(q)}">${esc(q)}</button>`).join('')}</div><details class="agent-privacy"><summary>助手会使用哪些资料？</summary><p>使用外部模型时，问题、相关记忆及工具返回的脱敏资料会发给所选服务商。查询已有分析不会重新付费分析岗位；修改记录前会先展示预览，等你确认。</p></details><div id="agent-context-note" class="meta" aria-live="polite"></div></section><section id="agent-result" class="agent-result" aria-live="polite"></section>`;
    if(!set(`${heading}<p class="agent-page-intro">用已有资料和记录，帮你把求职的下一步理清楚。</p>${CampusAgentHarness.markup({composer,workspace:CampusAgentWorkspace.markup()})}`))return;
    $('#agent-model-settings').onclick=()=>page('models').catch(fail);
    for(const b of box.querySelectorAll('[data-example]'))b.onclick=()=>{$('#ask').elements.message.value=b.dataset.example;$('#ask').elements.message.focus();};
    const agentDrafts=CampusNavigation.forms(document,{selector:'#memory-editor, #agent-skill-form, #agent-connector-form',ignoreNames:['skill_id']});agentDrafts.restore();CampusNavigation.register({active:()=>version===pageVersion,dirty:()=>agentDrafts.dirty()});
    const workspace=await CampusAgentWorkspace.bind({api,esc,formAction,UserError,fail,drafts:agentDrafts,active:()=>version===pageVersion});
    if(version!==pageVersion)return;
    const harness=await CampusAgentHarness.bind({api,esc,formAction,UserError,fail,D,renderData:translated,navigate:page,drafts:agentDrafts,user:capabilities.user_id,initialSkill:query?.skill,active:()=>version===pageVersion});
    if(version!==pageVersion)return;
    formAction('#ask',async (data,form)=>{const button=form.querySelector('button[type="submit"]');button.textContent='正在查询…';form.setAttribute('aria-busy','true');try{const result=await api('/agent/decide','POST',{...harness.options(),retrieval:CampusKnowledge.requestOptions(),task_id:workspace.taskID()||undefined,session_id:CampusModels.sessionID(capabilities),message:data.get('message'),model_config:CampusModels.requestConfig(),mask_name:CampusMatching.matchIdentity().mask_name});if(version!==pageVersion)return;harness.showQuestion();renderAgent(result,$('#agent-result'),()=>workspace.refresh());await Promise.all([workspace.refresh(),harness.refresh()]);}finally{button.textContent='查询求职记录';form.removeAttribute('aria-busy');}});return;
  }
  if (name==='profile') {const helpers={api,esc,D,formAction,UserError,navigate:page,active:()=>version===pageVersion};if(query?.evidence)await CampusEvidence.page(set,heading,helpers,query.evidence);else await CampusProfile.page(set,heading,helpers);return;}
  if (name==='interviews') {await CampusInterviews.page(set,heading,{api,esc,D,formAction,navigate:page,reviewInterview,initialInterview:query?.interviewID||'',initialView:query?.view||'',active:()=>version===pageVersion});return;}
  if (name==='applications') {await CampusApplications.page(set,heading,{api,esc,D,formAction,navigate:page,scheduleInterview,table,initialApplication:query?.applicationID||'',active:()=>version===pageVersion});return;}
  if(name==='ingest') {
    set(`${heading}<p>粘贴招聘说明，或填写公开招聘页面的网址。手动录入的信息需要核验；遇到登录或验证码限制时，仅记录访问情况。</p><form id="ingest" novalidate><div class="form-grid">${input('company','公司名称','','text','required')}${input('title','岗位名称','','text','required')}${input('locations','工作地点（多项用顿号分隔）','','text','required')}<label>岗位类型<select name="job_type">${options('job_type','FULL_TIME')}</select></label></div>${input('url','公开招聘页面网址（可选）','','url','placeholder="粘贴公开招聘页面的网址"')}${area('text','岗位招聘说明','','maxlength="60000" placeholder="粘贴岗位说明；如已填写网址，可留空以获取公开页面。"')}<button>保存岗位观察</button></form><div id="ingest-result"></div>`);
    formAction('#ingest',async data=>{const body=Object.fromEntries(data);body.locations=D.parseList(body.locations);if(!body.text.trim()&&!body.url)throw new UserError('请粘贴岗位说明，或填写公开招聘页面的网址。');const result=await api('/api/ingest','POST',body);if(version===pageVersion)$('#ingest-result').innerHTML=`<h3>观察记录已保存</h3><p>后续会分析证据并更新判断。获取成功不代表岗位一定可投递。</p>${translated(result)}`;});return;
  }
  const rows=await api('/api/'+name);if(version!==pageVersion)return;
  if(name==='weak_topics') {set(`${heading}<p>根据面试复盘累计，帮助你找到反复卡住的知识点。加强程度越高，越值得优先复习。</p>${table(rows,['topic','weight','occurrence_count','first_seen','last_seen','evidence_sources'],'还没有待加强知识点。完成面试复盘后，在这里查看需要重点补齐的内容。')}`);return;}
  if(name==='project_facts') {set(`${heading}<p>面试中只把已核验、已实现的内容当作项目成果。局限和计划会单独标注，避免把设想说成经历。</p>${rows.length?rows.map(f=>`<article class="card"><h3>${pill(f.kind,'fact')} · ${f.verified?'已核验':'待核验'}</h3><p>${esc(D.text(f.claim))}</p><p class="meta">项目编号：${esc(f.project_id)} · 更新于 ${esc(D.date(f.updated_at))}</p>${f.reference?`<p>依据：${esc(D.text(f.reference))}</p>`:''}</article>`).join(''):empty('还没有项目事实。添加并核验项目内容后，可用于面试准备。')}`);}
}
function protectEditor(selector,version) {
  const drafts=CampusNavigation.forms(document,{selector});drafts.restore();CampusNavigation.register({active:()=>version===pageVersion,dirty:()=>drafts.dirty()});return drafts;
}
async function scheduleInterview(applicationID) {
  if(!await CampusNavigation.leave())return;
  const version=++pageVersion;document.title='记录面试安排 · CampusTrace';$('#crumb').textContent='面试安排';$('#content').innerHTML=empty('正在读取投递记录…');
  const application=await api('/api/applications/'+encodeURIComponent(applicationID));if(version!==pageVersion)return;const job=application?.job;
  $('#content').innerHTML=`<button id="back-app">返回投递进展</button><h2>记录面试安排</h2>${job?`<p class="workflow-context">${esc(job.company)} · ${esc(job.title)}</p>`:''}<p>按收到的面试通知填写，时间使用北京时间。</p><form id="schedule" novalidate>${input('round','第几轮面试',1,'number','required min="1" max="20"')}${input('scheduled_at','面试时间（北京时间）','','datetime-local','required')}${area('notes','面试备注（可选）')}<button>保存面试安排</button></form>`;
  const drafts=protectEditor('#schedule',version);
  $('#back-app').onclick=()=>page('applications',{applicationID}).catch(fail);
  formAction('#schedule',async data=>{const point=drafts.savepoint('#schedule');let scheduled;try{scheduled=D.shanghaiISO(data.get('scheduled_at'));}catch(e){throw new UserError(e.message);}await api('/api/interviews','POST',{application_id:applicationID,round:Number(data.get('round')),scheduled_at:scheduled,result:'PENDING',notes:data.get('notes')});if(version!==pageVersion)return;drafts.saved('#schedule',point);await page('interviews');});
}
async function reviewInterview(interviewID) {
  if(!await CampusNavigation.leave())return;
  const version=++pageVersion;document.title='填写面试复盘 · CampusTrace';$('#crumb').textContent='面试复盘';$('#content').innerHTML=empty('正在读取面试记录…');
  await CampusAssets.ensure('drafts');if(version!==pageVersion)return;
  const [interview,capabilities]=await Promise.all([api('/api/interviews/'+encodeURIComponent(interviewID)),api('/api/profile/resume/capabilities')]);if(version!==pageVersion)return;const job=interview?.job;
  $('#content').innerHTML=`<button id="back-interviews">返回面试与复盘</button><h2>填写本轮面试复盘</h2>${job?`<p class="workflow-context">${esc(job.company)} · ${esc(job.title)} · 第 ${Number(interview.round)} 轮</p>`:''}<p>记录真实被问到的问题和自己的表现，不补写未发生的经历。问题和未答好的要点请每行填写一项。</p><section id="local-drafts" class="local-drafts" aria-label="面试复盘草稿"></section><form id="review" novalidate>${area('actual_questions','实际被问到的问题','','required placeholder="例如：如何恢复任务队列中尚未确认的消息？"')}${area('self_evaluation','自我复盘','','required placeholder="哪些部分回答清楚了？哪里还不熟悉？"')}${area('missed_points','未答好的要点')}${area('follow_up_notes','后续复习计划')}<fieldset><legend>待加强知识点（可选）</legend><p>每条知识点都要有本轮复盘依据；依据请摘自上方的问题、自我复盘或未答好的要点。</p><div id="topic-rows"></div><button type="button" id="add-topic">添加知识点</button></fieldset><button>保存本轮复盘</button></form>`;
  const drafts=protectEditor('#review',version);
  $('#back-interviews').onclick=()=>page('interviews').catch(fail);
  const addTopic=()=>{const row=document.createElement('div');row.className='topic-row';row.innerHTML=`${input('topic','知识点名称','','text','required maxlength="120"')}${input('weight','加强程度（1 较轻，5 需重点加强）',3,'number','required min="1" max="5"')}${input('evidence','本轮复盘依据（摘录原句）','','text','required')}<button type="button">移除此知识点</button>`;row.querySelector('button').onclick=()=>row.remove();$('#topic-rows').append(row);};
  $('#add-topic').onclick=addTopic;
  const localDrafts=globalThis.CampusDrafts?.mount({user:capabilities.user_id,scope:'review-'+interviewID,tracker:drafts,allow:id=>id==='review',active:()=>version===pageVersion,prepare:(form,values)=>{for(let i=0;i<values.filter(v=>v.name==='topic').length;i++)addTopic();}});
  CampusNavigation.register({active:()=>version===pageVersion,dirty:()=>drafts.dirty(),checkpoint:()=>localDrafts?.flush(),dispose:()=>localDrafts?.dispose()});
  formAction('#review',async(data,form)=>{const point=drafts.savepoint('#review');const lines=k=>String(data.get(k)||'').split('\n').map(s=>s.trim()).filter(Boolean);const weak=[...form.querySelectorAll('.topic-row')].map(row=>({topic:row.querySelector('[name="topic"]').value,weight:Number(row.querySelector('[name="weight"]').value),evidence:row.querySelector('[name="evidence"]').value}));await api('/api/reviews','POST',{interview_id:interviewID,actual_questions:lines('actual_questions'),self_evaluation:data.get('self_evaluation'),missed_points:lines('missed_points'),follow_up_notes:data.get('follow_up_notes'),weak_topics:weak});if(version!==pageVersion)return;drafts.saved('#review',point);localDrafts?.flush();await page('interviews');});
}
function renderAgent(result,box,onConfirmed) {
  const terminal=result.terminal_reason;
  const notices={COMPLETED:'已核对本次查询所需的记录，结果与依据如下。',BUDGET_LIMIT:'已达到本次上下文或调用预算。以下仅展示已取得的依据，可缩小范围后再问。',UNGROUNDED:'没有查到足够的可信依据，暂时无法确认。',ERROR:'本次查询未能完成，请稍后重试。',TIMEOUT:'本次查询用时较长，已停止。可缩小问题范围后再试。',CANCELLED:'本次查询已取消。',TOOL_LIMIT:'已达到本次查询上限。以下仅展示已经取得的记录，未执行的操作不会生效。',STEP_LIMIT:'已达到本次分析上限，最后提出的操作未执行。以下仅展示已经取得的依据。'};
  // Render original structured observations in Chinese; leave API answer/contract untouched.
  box.innerHTML=`<h3>${esc(D.label('terminal',terminal))}</h3><p>${esc(notices[terminal]||'本次查询暂时无法完成，请稍后重试。')}</p>${result.answer?`<div class="agent-answer">${esc(D.text(result.answer))}</div>`:''}${result.memory_notice?`<p class="callout">${esc(result.memory_notice)}</p>`:''}<p class="meta">分析 ${Number(result.model_steps)||0} 轮 · 查询资料或生成预览 ${Number(result.executed_tool_count)||0} 次</p>`;
  if(result.route){const route={LOCAL_LOOKUP:'本地记录查询',PRIMARY:'当前模型',COMPLEX:'复杂问题模型'}[result.route]||'当前执行方式';box.insertAdjacentHTML('beforeend',`<p class="meta">${esc(route)} · 外部模型请求 ${Number(result.provider_calls)||0} 次${CampusAgentHarness.usageText(result)?' · '+esc(CampusAgentHarness.usageText(result)):''}</p>`);}
  box.insertAdjacentHTML('beforeend',CampusAgentActions.markup(result.next_actions,esc));CampusAgentActions.bind(box,result.next_actions,{navigate:page,fail,active:()=>box.isConnected});
  if(result.context)box.insertAdjacentHTML('beforeend',`<details><summary>本次用了哪些上下文和工具</summary><p>带入 ${result.context.memory_ids?.length||0} 条相关记忆、${Number(result.context.history_turns)||0} 轮历史。</p><p>${(result.context.available_tools||[]).map(v=>esc(D.label('tool',v))).join('、')}</p></details>`);
  for(const item of result.grounded_observations||[]) {
    const article=document.createElement('article');article.className='card';const pending=item.data?.action_id;const memoryAction=item.data?.action_type==='remember_memory';
    const details=document.createElement('details');details.open=!!pending;
    details.innerHTML=`<summary>${esc(D.label('tool',item.tool))} · 查看记录与依据</summary>${pending?'<p class="pending-note">这是待确认预览，尚未修改你的求职记录。请核对内容后再确认。</p>':''}${memoryAction?CampusAgentWorkspace.preview(item.data,esc):translated(item.data)}`;
    if(pending){const b=document.createElement('button');b.textContent='确认执行此操作';b.onclick=async()=>{b.disabled=true;try{const v=await api('/agent/actions/'+encodeURIComponent(pending)+'/confirm','POST',{confirm:true});details.querySelector('.pending-note').textContent='已确认执行，保存结果如下。';b.textContent='操作已确认';details.insertAdjacentHTML('beforeend',`<h4>保存结果</h4>${memoryAction?CampusAgentWorkspace.preview(v,esc):translated(v)}`);try{await onConfirmed?.();}catch(refreshError){$('#notice').textContent='操作已保存，但列表暂时无法刷新。可重新打开此页查看。';}}catch(error){b.disabled=false;fail(error);}};details.append(b);}
    article.append(details);
    if(item.tool==='get_match_result'&&/^[a-f0-9]{32}$/.test(item.data?.job_id||'')){const b=document.createElement('button');b.className='btn btn-small';b.textContent='在岗位库核对';b.onclick=()=>page('matching',{jobID:item.data.job_id}).catch(fail);article.append(b);}
    box.append(article);
  }
}
async function detail(id) {
  if(!await CampusNavigation.leave())return;
  CampusUI.closeAll();$('#crumb').textContent='岗位核验记录';
  const version=++pageVersion;window.scrollTo(0,0);$('#notice').textContent='';$('#content').innerHTML=empty('正在核对岗位记录…');
  await Promise.all([CampusAssets.ensure('matching'),CampusAssets.ensure('radar')]);if(version!==pageVersion)return;
  const v=await api('/api/jobs/'+id);if(version!==pageVersion)return;const j=v.job,officialURL=CampusApplications.officialLink(v.official_url);document.title=`${D.text(j.title)} · 岗位详情 · CampusTrace`;
  $('#content').innerHTML=`<button id="back">← 返回岗位库</button><h2>${esc(D.text(j.title))} ${pill(j.current_status)}</h2><p>${esc(D.text(j.company))} · ${esc((j.locations||[]).map(D.text).join('、')||'地点待确认')} · ${esc(D.label('job_type',j.job_type))}</p><p class="meta">岗位编号：${esc(id)} · 最近更新：${esc(D.date(j.updated_at))}</p><div class="actions">${officialURL?`<a class="btn" href="${esc(officialURL)}" target="_blank" rel="noopener noreferrer" aria-label="在新标签页查看官网岗位">${CampusUI.icon('external')}查看官网岗位</a>`:'<span class="meta">暂未记录岗位网页链接</span>'}<button data-pref="SAVED" data-id="${esc(id)}">稍后看</button><button data-pref="IGNORED" data-id="${esc(id)}">忽略</button><button data-apply="APPLIED" data-id="${esc(id)}">已投递</button><button id="apply">加入投递计划</button><button id="prepare">岗位准备清单</button></div><article id="skill-match" class="card"><h3>技能与项目匹配</h3><p>正在读取匹配记录…</p></article><div class="grid"><article><h3>基础投递条件核对 ${pill(v.eligibility?.status||'UNKNOWN','eligibility')}</h3>${v.eligibility?table(v.eligibility.results,['rule','result','requirement','candidate_value','explanation']):empty('请先完善求职资料，再核对毕业届别、学历等投递要求。')}</article><article><h3>Go 技术方向匹配</h3><p>${pill(v.go_fit||'UNKNOWN','fit')}</p><h3>个人偏好匹配分：${Number.isFinite(v.ranking?.score)?v.ranking.score.toFixed(1):'暂未计算'}</h3><p class="meta">这是可解释的偏好排序分，不代表录用概率。城市、岗位类型和职位偏好使用岗位元数据；状态与资格使用证据评估。</p>${v.ranking?Object.entries(v.ranking.breakdown).map(([k,n])=>`<p>${esc(D.label('ranking',k))}：${Number(n).toFixed(1)} 分 <small>${esc(D.text(v.ranking.breakdown_sources?.[k]||'依据待核验'))}</small></p><progress aria-label="${esc(D.label('ranking',k))}" max="100" value="${Number(n)}"></progress>`).join(''):empty('完善求职资料后可查看分项得分。')}</article></div><h3>岗位观察与证据时间线</h3><p class="meta">每次访问单独留档。原文摘录保留来源语言；获取成功不等于仍可投递。</p><div class="timeline">${v.observations?.length?v.observations.map(o=>`<article><small>${esc(D.date(o.observed_at))} · ${esc(D.label('trust',o.trust))}</small><h4>${pill(o.fetch_status,'fetch')} · ${pill(o.extraction_status,'extraction')}</h4><p class="meta">页面响应码：${o.http_status||'不适用'} · 内容指纹：${esc(o.normalized_content_hash||'本次未取得内容')}</p><details><summary>查看招聘原文（保留来源语言）</summary><pre>${esc(o.text||'本次未取得可用的招聘原文。')}</pre></details>${v.evidence.filter(e=>e.observation_id===o.id).map(e=>`<div class="evidence"><p><b>${esc(D.label('evidence',e.type))}</b>：${esc(D.requirement(e.value,e.type))}</p><blockquote><small>证据原文：</small>${esc(e.excerpt)}</blockquote><p class="meta">${esc(D.label('method',e.extraction_method))} · 置信度 ${Math.round(e.confidence*100)}% · 证据编号：${esc(e.id)} · 提取版本：${esc(D.text(e.analysis_version||'历史版本未记录'))}</p></div>`).join('')||empty('本次观察尚未形成可用证据。')}</article>`).join(''):empty('暂无岗位观察记录。')}</div><h3>岗位状态判断历史</h3>${table(v.assessments,['status','rule_version','assessed_at','reason','evidence_ids'],'暂无状态判断记录，暂时无法确认是否可投递。')}<h3>岗位信息变化</h3>${table(v.changes,['type','created_at','from_observation','to_observation'],'暂未发现已分析版本之间的内容变化。')}<div id="prep"></div>`;
  CampusMatching.showJob($('#skill-match'),id,{api,esc,D,navigate:page}).catch(error=>{if(version===pageVersion)$('#skill-match').innerHTML=`<h3>技能与项目匹配</h3><p>${esc(error.message)}</p>`;});
  bindRadar($('#content'),()=>detail(id));
  $('#back').onclick=()=>page('jobs').catch(fail);
  $('#apply').onclick=async()=>{try{await api('/api/applications','POST',{job_id:id});await page('applications');}catch(error){fail(error);}};
  $('#prepare').onclick=()=>page('matching',{jobID:id,view:'preparation'}).catch(fail);
}
show();
