'use strict';
const D = CampusDisplay;
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
  if (!response.ok) { const details=await response.json().catch(()=>({}));const error=new UserError(details.code==='CORPUS_CAPACITY'?'资料库超过当前 10,000 个片段的可检索容量；本次导入或检索未执行。':D.error(response.status,path));error.status=response.status;throw error; }
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
function show() { $('#auth').hidden=!!token;$('#workspace').hidden=!token;document.title='CampusTrace · 校招求职记录';if(token)page('jobs').catch(fail); }
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
$('#logout').onclick=()=>{pageVersion++;token='';sessionStorage.removeItem('campustrace-token');$('#content').replaceChildren();$('#notice').textContent='';show();};
for (const b of document.querySelectorAll('[data-page]')) b.onclick=()=>page(b.dataset.page).catch(fail);
const pageTitles={jobs:'校招岗位',applications:'投递进展',interviews:'面试与复盘',weak_topics:'待加强知识点',project_facts:'项目事实',agent:'求职问答',profile:'求职资料',ingest:'录入岗位'};
function input(name,label,value='',type='text',extra='') {return `<label>${esc(label)}<input name="${esc(name)}" type="${type}" value="${esc(value)}" ${extra}></label>`;}
function area(name,label,value='',extra='') {return `<label>${esc(label)}<textarea name="${esc(name)}" ${extra}>${esc(value)}</textarea></label>`;}
function displayQuery(query) {
  const samples={'雪松':'Synthetic Cedar','港湾':'Synthetic Harbor','枫叶':'Synthetic Maple','青松':'Synthetic Pine','后端':'backend','实习':'intern'};
  return samples[query.trim()]||query;
}
async function page(name,query='') {
  const version=++pageVersion;$('#notice').textContent='';const box=$('#content');box.innerHTML=empty('正在读取记录…');
  document.title=`${pageTitles[name]||'求职记录'} · CampusTrace`;
  const set=html=>{if(version!==pageVersion)return false;box.innerHTML=html;return true;};
  const heading=`<h2>${pageTitles[name]}</h2>`;
  if (name==='jobs') {
    const jobs=await api('/api/jobs?q='+encodeURIComponent(displayQuery(query)));
    if(!set(`${heading}<p class="meta">先核对岗位是否可投递，再结合个人条件与意向做选择。带“虚构演示”标记的公司与岗位仅用于演示。</p><form id="search" novalidate><label>岗位或公司<input name="q" placeholder="例如：后端、Go、雪松" value="${esc(query)}"></label><button>搜索岗位</button><small>最多显示 100 条，请用关键词缩小范围。</small></form><button id="refresh">刷新列表</button>${jobs.length?`<div class="table-wrap"><table><thead><tr><th>岗位</th><th>公司</th><th>工作地点</th><th>投递状态</th></tr></thead><tbody>${jobs.map(j=>`<tr><td><button data-job="${esc(j.id)}">${esc(D.text(j.title))}</button></td><td>${esc(D.text(j.company))}</td><td>${esc((j.locations||[]).map(D.text).join('、')||'地点待确认')}</td><td>${pill(j.current_status)}</td></tr>`).join('')}</tbody></table></div>`:empty('没有找到相关岗位，试试其他公司名或岗位关键词。')}`))return;
    formAction('#search',data=>page('jobs',data.get('q')));$('#refresh').onclick=()=>page('jobs',query).catch(fail);
    for(const b of box.querySelectorAll('[data-job]'))b.onclick=()=>detail(b.dataset.job).catch(fail);return;
  }
  if (name==='agent') {
    set(`${heading}<p>根据岗位证据和你的求职记录回答问题。涉及投递进展或面试复盘的修改，先展示预览，再由你确认。</p><div class="examples" aria-label="试着这样问"><span>试着这样问：</span>${['我投过哪些岗位？','我的项目做了什么？','我有哪些薄弱知识点？'].map(q=>`<button type="button" data-example="${esc(q)}">${esc(q)}</button>`).join('')}</div><form id="ask" novalidate>${area('message','你的问题','','required maxlength="4000" placeholder="例如：为什么这个岗位可投递？请附上岗位编号，便于核对证据。"')}<button>查询求职记录</button></form><div id="agent-result" aria-live="polite"></div>`);
    for(const b of box.querySelectorAll('[data-example]'))b.onclick=()=>{$('#ask').elements.message.value=b.dataset.example;$('#ask').elements.message.focus();};
    formAction('#ask',async data=>{const result=await api('/agent/decide','POST',{session_id:'web',message:data.get('message')});if(version!==pageVersion)return;renderAgent(result,$('#agent-result'));});return;
  }
  if (name==='profile') {
    let profile={};try {profile=await api('/api/profile');}catch(error){if(error.status!==404)throw error;}
    if(!set(`${heading}<p>如实填写毕业时间、学历和求职意向，用于逐项核对校招要求。留空表示尚未填写，不代表自动满足。</p><form id="save-profile" novalidate><div class="form-grid">${input('graduation_year','毕业届别（年份）',profile.graduation_year||'','number','min="2000" max="2100" placeholder="例如：2027"')}${input('graduation_from','毕业年份范围起点（届别未定时填写）',profile.graduation_from||'','number','min="2000" max="2100"')}${input('graduation_to','毕业年份范围终点',profile.graduation_to||'','number','min="2000" max="2100"')}<label>最高学历<select name="degree">${options('degree',profile.degree,true)}</select></label>${input('experience_months','相关实习或工作经验（月）',profile.experience_months??0,'number','min="0"')}</div><fieldset><legend>意向岗位类型</legend>${Object.entries(D.enums.job_type).filter(([v])=>v!=='UNKNOWN').map(([v,label])=>`<label class="check"><input name="preferred_job_types" type="checkbox" value="${v}" ${(profile.preferred_job_types||[]).includes(v)?'checked':''}>${label}</label>`).join('')}</fieldset><p class="meta">多项内容可用顿号或逗号分隔。</p><div class="form-grid">${['majors','preferred_cities','acceptable_cities','target_roles','technical_skills','target_languages'].map(k=>input(k,D.field(k),D.inputList(profile[k]))).join('')}</div><button>保存求职资料</button></form>`))return;
    formAction('#save-profile',async data=>{const body={...profile};for(const k of ['graduation_year','graduation_from','graduation_to','experience_months'])body[k]=Number(data.get(k)||0);body.degree=data.get('degree');body.preferred_job_types=data.getAll('preferred_job_types');for(const k of ['majors','preferred_cities','acceptable_cities','target_roles','technical_skills','target_languages']){const raw=data.get(k);body[k]=raw===D.inputList(profile[k])?(profile[k]||[]):D.parseList(raw);}await api('/api/profile','PUT',body);$('#notice').textContent='求职资料已保存。';});return;
  }
  if(name==='ingest') {
    set(`${heading}<p>粘贴招聘说明，或填写公开招聘页面的网址。手动录入的信息需要核验；遇到登录或验证码限制时，仅记录访问情况。</p><form id="ingest" novalidate><div class="form-grid">${input('company','公司名称','','text','required')}${input('title','岗位名称','','text','required')}${input('locations','工作地点（多项用顿号分隔）','','text','required')}<label>岗位类型<select name="job_type">${options('job_type','FULL_TIME')}</select></label></div>${input('url','公开招聘页面网址（可选）','','url','placeholder="粘贴公开招聘页面的网址"')}${area('text','岗位招聘说明','','maxlength="60000" placeholder="粘贴岗位说明；如已填写网址，可留空以获取公开页面。"')}<button>保存岗位观察</button></form><div id="ingest-result"></div>`);
    formAction('#ingest',async data=>{const body=Object.fromEntries(data);body.locations=D.parseList(body.locations);if(!body.text.trim()&&!body.url)throw new UserError('请粘贴岗位说明，或填写公开招聘页面的网址。');const result=await api('/api/ingest','POST',body);if(version===pageVersion)$('#ingest-result').innerHTML=`<h3>观察记录已保存</h3><p>后续会分析证据并更新判断。获取成功不代表岗位一定可投递。</p>${translated(result)}`;});return;
  }
  const rows=await api('/api/'+name);if(version!==pageVersion)return;
  if(name==='applications') {
    set(`${heading}<p>按自己的实际投递情况记录进展。岗位关闭不会自动终止已提交的申请。</p>${rows.length?rows.map(a=>`<article class="card"><h3>岗位编号：${esc(a.job_id)} ${pill(a.current_state,'application')}</h3><p class="meta">记录版本：${a.version} · 最近更新：${esc(D.date(a.updated_at))}</p><div class="actions"><button data-history="${a.id}">查看进展记录</button><label>调整到<select id="state-${a.id}">${options('application',a.current_state)}</select></label><button data-transition="${a.id}">更新投递进展</button><button data-interview="${a.id}">记录面试安排</button></div><div id="history-${a.id}"></div></article>`).join(''):empty('还没有投递记录。可在岗位详情中将心仪岗位加入投递计划。')}`);
    for(const a of rows){
      $(`[data-history="${a.id}"]`).onclick=async()=>{try{const history=await api('/api/applications/'+a.id+'/history');if(version===pageVersion)$('#history-'+a.id).innerHTML=`<h4>投递进展记录</h4>${table(history,['from_state','to_state','occurred_at','note'],'暂未记录进展变化。')}`;}catch(error){fail(error);}};
      $(`[data-transition="${a.id}"]`).onclick=async()=>{try{await api('/api/applications/transition','POST',{application_id:a.id,state:$('#state-'+a.id).value,version:a.version});await page('applications');}catch(error){fail(error);}};
      $(`[data-interview="${a.id}"]`).onclick=()=>scheduleInterview(a.id);
    }return;
  }
  if(name==='interviews') {
    const reviews=await api('/api/reviews');if(version!==pageVersion)return;
    set(`${heading}<p>记录每轮面试的时间、结果与真实问题，把未答好的要点转成后续复习重点。时间统一显示为北京时间。</p>${rows.length?rows.map(v=>{const review=reviews.find(r=>r.interview_id===v.id);return `<article class="card"><h3>第 ${v.round} 轮面试 ${pill(v.result,'interview')}</h3><p>面试时间：${esc(D.date(v.scheduled_at))}</p><p>${esc(D.text(v.notes))}</p>${v.finished_at?`<p class="meta">完成时间：${esc(D.date(v.finished_at))}</p>`:''}${review?`<details><summary>查看本轮复盘</summary>${table([review],['actual_questions','self_evaluation','missed_points','follow_up_notes'])}</details>`:`<button data-review="${v.id}">填写本轮复盘</button>`}</article>`;}).join(''):empty('还没有面试安排。可从投递进展中记录收到的面试通知。')}`);
    for(const b of box.querySelectorAll('[data-review]'))b.onclick=()=>reviewInterview(b.dataset.review);return;
  }
  if(name==='weak_topics') {set(`${heading}<p>根据面试复盘累计，帮助你找到反复卡住的知识点。加强程度越高，越值得优先复习。</p>${table(rows,['topic','weight','occurrence_count','first_seen','last_seen','evidence_sources'],'还没有待加强知识点。完成面试复盘后，在这里查看需要重点补齐的内容。')}`);return;}
  if(name==='project_facts') {set(`${heading}<p>面试中只把已核验、已实现的内容当作项目成果。局限和计划会单独标注，避免把设想说成经历。</p>${rows.length?rows.map(f=>`<article class="card"><h3>${pill(f.kind,'fact')} · ${f.verified?'已核验':'待核验'}</h3><p>${esc(D.text(f.claim))}</p><p class="meta">项目编号：${esc(f.project_id)} · 更新于 ${esc(D.date(f.updated_at))}</p>${f.reference?`<p>依据：${esc(D.text(f.reference))}</p>`:''}</article>`).join(''):empty('还没有项目事实。添加并核验项目内容后，可用于面试准备。')}`);}
}
function scheduleInterview(applicationID) {
  ++pageVersion;document.title='记录面试安排 · CampusTrace';$('#content').innerHTML=`<button id="back-app">返回投递进展</button><h2>记录面试安排</h2><p>按收到的面试通知填写，时间使用北京时间。</p><form id="schedule" novalidate>${input('round','第几轮面试',1,'number','required min="1" max="20"')}${input('scheduled_at','面试时间（北京时间）','','datetime-local','required')}${area('notes','面试备注（可选）')}<button>保存面试安排</button></form>`;
  $('#back-app').onclick=()=>page('applications').catch(fail);
  formAction('#schedule',async data=>{let scheduled;try{scheduled=D.shanghaiISO(data.get('scheduled_at'));}catch(e){throw new UserError(e.message);}await api('/api/interviews','POST',{application_id:applicationID,round:Number(data.get('round')),scheduled_at:scheduled,result:'PENDING',notes:data.get('notes')});await page('interviews');});
}
function reviewInterview(interviewID) {
  ++pageVersion;document.title='填写面试复盘 · CampusTrace';$('#content').innerHTML=`<button id="back-interviews">返回面试与复盘</button><h2>填写本轮面试复盘</h2><p>记录真实被问到的问题和自己的表现，不补写未发生的经历。问题和未答好的要点请每行填写一项。</p><form id="review" novalidate>${area('actual_questions','实际被问到的问题','','required placeholder="例如：如何恢复任务队列中尚未确认的消息？"')}${area('self_evaluation','自我复盘','','required placeholder="哪些部分回答清楚了？哪里还不熟悉？"')}${area('missed_points','未答好的要点')}${area('follow_up_notes','后续复习计划')}<fieldset><legend>待加强知识点（可选）</legend><p>每条知识点都要有本轮复盘依据；依据请摘自上方的问题、自我复盘或未答好的要点。</p><div id="topic-rows"></div><button type="button" id="add-topic">添加知识点</button></fieldset><button>保存本轮复盘</button></form>`;
  $('#back-interviews').onclick=()=>page('interviews').catch(fail);
  $('#add-topic').onclick=()=>{const row=document.createElement('div');row.className='topic-row';row.innerHTML=`${input('topic','知识点名称','','text','required maxlength="120"')}${input('weight','加强程度（1 较轻，5 需重点加强）',3,'number','required min="1" max="5"')}${input('evidence','本轮复盘依据（摘录原句）','','text','required')}<button type="button">移除此知识点</button>`;row.querySelector('button').onclick=()=>row.remove();$('#topic-rows').append(row);};
  formAction('#review',async(data,form)=>{const lines=k=>String(data.get(k)||'').split('\n').map(s=>s.trim()).filter(Boolean);const weak=[...form.querySelectorAll('.topic-row')].map(row=>({topic:row.querySelector('[name="topic"]').value,weight:Number(row.querySelector('[name="weight"]').value),evidence:row.querySelector('[name="evidence"]').value}));await api('/api/reviews','POST',{interview_id:interviewID,actual_questions:lines('actual_questions'),self_evaluation:data.get('self_evaluation'),missed_points:lines('missed_points'),follow_up_notes:data.get('follow_up_notes'),weak_topics:weak});await page('weak_topics');});
}
function renderAgent(result,box) {
  const terminal=result.terminal_reason;
  const notices={COMPLETED:'已核对本次查询所需的记录，结果与依据如下。',UNGROUNDED:'没有查到足够的可信依据，暂时无法确认。',ERROR:'本次查询未能完成，请稍后重试。',TIMEOUT:'本次查询用时较长，已停止。可缩小问题范围后再试。',CANCELLED:'本次查询已取消。',TOOL_LIMIT:'已达到本次查询上限。以下仅展示已经取得的记录，未执行的操作不会生效。',STEP_LIMIT:'已达到本次分析上限，最后提出的操作未执行。以下仅展示已经取得的依据。'};
  // Render original structured observations in Chinese; leave API answer/contract untouched.
  box.innerHTML=`<h3>${esc(D.label('terminal',terminal))}</h3><p>${esc(notices[terminal]||'本次查询暂时无法完成，请稍后重试。')}</p><p class="meta">分析 ${Number(result.model_steps)||0} 轮 · 查询资料或生成预览 ${Number(result.executed_tool_count)||0} 次</p>`;
  for(const item of result.grounded_observations||[]) {
    const article=document.createElement('article');article.className='card';const pending=item.data?.action_id;
    article.innerHTML=`<h4>${esc(D.label('tool',item.tool))}</h4>${pending?'<p class="pending-note">这是待确认预览，尚未修改你的求职记录。请核对内容后再确认。</p>':''}${translated(item.data)}`;
    if(pending){const b=document.createElement('button');b.textContent='确认执行此操作';b.onclick=async()=>{b.disabled=true;try{const v=await api('/agent/actions/'+encodeURIComponent(pending)+'/confirm','POST',{confirm:true});article.querySelector('.pending-note').textContent='已确认执行，保存结果如下。';b.textContent='操作已确认';article.insertAdjacentHTML('beforeend',`<h4>保存结果</h4>${translated(v)}`);}catch(error){b.disabled=false;fail(error);}};article.append(b);}
    box.append(article);
  }
}
async function detail(id) {
  const version=++pageVersion;window.scrollTo(0,0);$('#notice').textContent='';$('#content').innerHTML=empty('正在核对岗位记录…');
  const v=await api('/api/jobs/'+id);if(version!==pageVersion)return;const j=v.job;document.title=`${D.text(j.title)} · 岗位详情 · CampusTrace`;
  $('#content').innerHTML=`<button id="back">← 返回校招岗位</button><h2>${esc(D.text(j.title))} ${pill(j.current_status)}</h2><p>${esc(D.text(j.company))} · ${esc((j.locations||[]).map(D.text).join('、')||'地点待确认')} · ${esc(D.label('job_type',j.job_type))}</p><p class="meta">岗位编号：${esc(id)} · 最近更新：${esc(D.date(j.updated_at))}</p><div class="actions"><button id="apply">加入投递计划</button><button id="prepare">查看面试准备建议</button></div><div class="grid"><article><h3>投递条件核对 ${pill(v.eligibility?.status||'UNKNOWN','eligibility')}</h3>${v.eligibility?table(v.eligibility.results,['rule','result','requirement','candidate_value','explanation']):empty('请先完善求职资料，再核对毕业届别、学历等投递要求。')}</article><article><h3>Go 技术方向匹配</h3><p>${pill(v.go_fit||'UNKNOWN','fit')}</p><h3>个人偏好匹配分：${Number.isFinite(v.ranking?.score)?v.ranking.score.toFixed(1):'暂未计算'}</h3><p class="meta">这是可解释的偏好排序分，不代表录用概率。城市、岗位类型和职位偏好使用岗位元数据；状态与资格使用证据评估。</p>${v.ranking?Object.entries(v.ranking.breakdown).map(([k,n])=>`<p>${esc(D.label('ranking',k))}：${Number(n).toFixed(1)} 分 <small>${esc(D.text(v.ranking.breakdown_sources?.[k]||'依据待核验'))}</small></p><progress aria-label="${esc(D.label('ranking',k))}" max="100" value="${Number(n)}"></progress>`).join(''):empty('完善求职资料后可查看分项得分。')}</article></div><h3>岗位观察与证据时间线</h3><p class="meta">每次访问单独留档。原文摘录保留来源语言；获取成功不等于仍可投递。</p><div class="timeline">${v.observations?.length?v.observations.map(o=>`<article><small>${esc(D.date(o.observed_at))} · ${esc(D.label('trust',o.trust))}</small><h4>${pill(o.fetch_status,'fetch')} · ${pill(o.extraction_status,'extraction')}</h4><p class="meta">页面响应码：${o.http_status||'不适用'} · 内容指纹：${esc(o.normalized_content_hash||'本次未取得内容')}</p><details><summary>查看招聘原文（保留来源语言）</summary><pre>${esc(o.text||'本次未取得可用的招聘原文。')}</pre></details>${v.evidence.filter(e=>e.observation_id===o.id).map(e=>`<div class="evidence"><p><b>${esc(D.label('evidence',e.type))}</b>：${esc(D.requirement(e.value,e.type))}</p><blockquote><small>证据原文：</small>${esc(e.excerpt)}</blockquote><p class="meta">${esc(D.label('method',e.extraction_method))} · 置信度 ${Math.round(e.confidence*100)}% · 证据编号：${esc(e.id)} · 提取版本：${esc(D.text(e.analysis_version||'历史版本未记录'))}</p></div>`).join('')||empty('本次观察尚未形成可用证据。')}</article>`).join(''):empty('暂无岗位观察记录。')}</div><h3>岗位状态判断历史</h3>${table(v.assessments,['status','rule_version','assessed_at','reason','evidence_ids'],'暂无状态判断记录，暂时无法确认是否可投递。')}<h3>岗位信息变化</h3>${table(v.changes,['type','created_at','from_observation','to_observation'],'暂未发现已分析版本之间的内容变化。')}<div id="prep"></div>`;
  $('#back').onclick=()=>page('jobs').catch(fail);
  $('#apply').onclick=async()=>{try{await api('/api/applications','POST',{job_id:id});await page('applications');}catch(error){fail(error);}};
  $('#prepare').onclick=async()=>{try{const preparation=await api('/api/jobs/'+id+'/preparation');if(version===pageVersion)$('#prep').innerHTML=`<h3>面试准备建议与依据</h3><p>结合岗位要求、真实项目事实和历史薄弱点安排复习，不编造经历或面试答案。</p>${translated(preparation)}`;}catch(error){fail(error);}};
}
show();
