(function(root){
'use strict';
const id=v=>typeof v==='string'&&/^[a-f0-9]{32}$/.test(v);
const text=(v,n)=>typeof v==='string'&&v.trim()&&new TextEncoder().encode(v).length<=n&&!/[\0\r\n]/.test(v);
// A server hint can only open one of these application pages. It cannot
// supply a URL, confirm a write, run a skill or launch paid analysis.
function destination(v){
 if(!v||typeof v!=='object')return null;
 switch(v.kind){
 case 'application_preparation':return id(v.application_id)?{page:'agent',query:{skill:{id:'application-prep',value:v.application_id}}}:null;
 case 'dossier':return id(v.application_id)?{page:'dossier',query:{applicationID:v.application_id}}:null;
 case 'job':return id(v.job_id)?{page:'matching',query:{jobID:v.job_id}}:null;
 case 'preparation':return id(v.job_id)?{page:'matching',query:{jobID:v.job_id,view:'preparation'}}:null;
 case 'company':return text(v.company,200)?{page:'company_decision',query:{company:v.company}}:null;
 case 'knowledge_source':return id(v.document_id)?{page:'knowledge',query:{documentID:v.document_id}}:null;
 case 'knowledge':return {page:'knowledge'};
 case 'review_topic':return text(v.topic,120)?{page:'agent',query:{skill:{id:'review-plan',value:v.topic}}}:null;
 case 'applications':return {page:'applications'};
 case 'interviews':return {page:'interviews'};
 case 'matching_tasks':return id(v.run_id)?{page:'matching',query:{taskID:v.run_id}}:null;
 default:return null;
 }
}
function accepted(rows){return (Array.isArray(rows)?rows:[]).filter(v=>destination(v)&&text(v.label,300)&&text(v.reason,500)).slice(0,6);}
function markup(rows,esc){const items=accepted(rows);return items.length?`<nav class="agent-next" aria-label="根据本次记录继续"><h4>接下来可以做</h4><ul>${items.map((v,i)=>`<li><button type="button" class="text-btn" data-agent-next="${i}">${esc(v.label)}</button><p>${esc(v.reason)}</p></li>`).join('')}</ul></nav>`:'';}
function bind(container,rows,{navigate,fail,active=()=>true}){const items=accepted(rows);for(const b of container.querySelectorAll('[data-agent-next]'))b.onclick=()=>{if(!active())return;const to=destination(items[Number(b.dataset.agentNext)]);if(to)Promise.resolve(navigate(to.page,to.query)).catch(fail);};}

function restartInput(v){
 if(!['STALE','FAILED','CANCELLED'].includes(v?.state)||!v.input)return null;
 const input=v.input;
 if(input.skill_id!==v.skill_id)return null;
 switch(v.skill_id){
 case 'daily-review':return {id:v.skill_id,value:''};
 case 'review-plan':return text(input.topic,500)?{id:v.skill_id,value:input.topic}:null;
 case 'company-choice':return text(input.company,200)?{id:v.skill_id,value:input.company}:null;
 case 'application-prep':return id(input.application_id)?{id:v.skill_id,value:input.application_id}:null;
 case 'interview-prep':return id(input.job_id)?{id:v.skill_id,value:input.job_id}:null;
 default:return null;
 }
}

function bindTargets(form,{api,active=()=>true}){
 const box=form.querySelector('#agent-target-picker'),value=form.elements.value;
 let kind='',revision=0,timer,companies=null;
 function setValue(v){value.value=v;value.dispatchEvent(new Event('input',{bubbles:true}));}
 function choose(job){setValue(job.id);box.querySelector('[data-target-selected]').innerHTML=`<strong></strong><button type="button" class="text-btn">重新选择</button>`;box.querySelector('[data-target-selected] strong').textContent=job.company+' · '+job.title;box.querySelector('[data-target-results]').replaceChildren();box.querySelector('[data-target-selected] button').onclick=()=>{setValue('');box.querySelector('[data-target-selected]').replaceChildren();box.querySelector('[data-target-search]').focus();};}
 async function activate(next){
  kind=next;const current=++revision;root.clearTimeout(timer);box.replaceChildren();box.hidden=!['job_id','application_id'].includes(kind);value.type=['job_id','application_id'].includes(kind)?'hidden':'text';value.removeAttribute('list');
  if(kind==='application_id'){
   box.innerHTML='<label>选择这次申请<select data-application-target><option value="">正在读取本人申请…</option></select></label><p class="meta">使用这次申请留档的 JD 和材料；没有历史快照时会明确提示。</p>';
   const select=box.querySelector('[data-application-target]');
   try{let rows=await api('/api/applications');if(!active()||current!==revision||kind!=='application_id')return;
    if(id(value.value)&&!rows.some(v=>v.id===value.value)){const target=await api('/api/applications/'+encodeURIComponent(value.value));if(!active()||current!==revision||kind!=='application_id')return;rows=[target,...rows];}
    select.replaceChildren();const blank=document.createElement('option');blank.value='';blank.textContent='请选择要准备的申请';select.append(blank);
    for(const row of rows){if(!id(row.id))continue;const option=document.createElement('option');option.value=row.id;option.textContent=(row.job?.company||'历史申请')+' · '+(row.job?.title||row.id.slice(-8))+' · '+(row.current_state||'');select.append(option);}
    select.value=value.value||'';if(select.value!==value.value)setValue('');select.onchange=()=>setValue(select.value);
   }catch{if(active()&&current===revision){select.replaceChildren();const option=document.createElement('option');option.value='';option.textContent='申请读取失败，请重新选择此任务重试';select.append(option);setValue('');}}
   return;
  }
  if(kind==='company'){
   try{if(!companies)companies=await api('/api/matching/company-catalog');if(!active()||current!==revision)return;const list=document.createElement('datalist');list.id='agent-company-options';for(const item of companies){const option=document.createElement('option');option.value=typeof item==='string'?item:item.company||'';list.append(option);}box.append(list);value.setAttribute('list',list.id);}catch{/* Company text remains usable when the optional list is unavailable. */}return;
  }
  if(kind!=='job_id')return;
  box.innerHTML='<label>搜索要准备的岗位<input type="search" data-target-search maxlength="200" placeholder="输入公司或岗位名称" autocomplete="off"></label><div data-target-selected class="agent-target-selected" aria-live="polite"></div><div data-target-results class="agent-target-results" aria-live="polite"><p class="meta">输入名称后选择岗位，不需要复制编号。</p></div>';
  const search=box.querySelector('[data-target-search]'),results=box.querySelector('[data-target-results]');
  search.oninput=()=>{
   const query=search.value.trim(),request=++revision;root.clearTimeout(timer);setValue('');box.querySelector('[data-target-selected]').replaceChildren();
   if(!query){results.innerHTML='<p class="meta">输入公司或岗位名称，最多展示 10 个结果。</p>';return;}
   if(new TextEncoder().encode(query).length>200){results.textContent='搜索文字最多 200 字节，请缩短。';return;}
   results.innerHTML='<p class="meta">正在查找本地岗位…</p>';
   timer=root.setTimeout(async()=>{
    try{const jobs=await api('/api/jobs?q='+encodeURIComponent(query));if(!active()||request!==revision||kind!=='job_id')return;results.replaceChildren();const valid=jobs.filter(j=>id(j.id)).slice(0,10);if(!valid.length){results.textContent='没有找到岗位，试试公司名称或较短的关键词。';return;}for(const job of valid){const b=document.createElement('button');b.type='button';b.className='agent-target-option';const title=document.createElement('strong'),meta=document.createElement('span');title.textContent=job.title;meta.textContent=job.company+' · '+(job.locations||[]).join(' / ');b.append(title,meta);b.onclick=()=>{++revision;root.clearTimeout(timer);choose(job);};results.append(b);}if(jobs.length>10){const note=document.createElement('p');note.className='meta';note.textContent='仅展示前 10 个，请继续输入公司或岗位关键词缩小范围。';results.append(note);}}
    catch{if(active()&&request===revision)results.textContent='岗位暂时无法读取，修改搜索文字后可重试。';}
   },300);
  };
  if(id(value.value)){
   try{const result=await api('/api/jobs/'+encodeURIComponent(value.value));if(active()&&current===revision&&kind==='job_id')choose(result.job);}catch{if(active()&&current===revision){setValue('');results.textContent='原岗位目前无法读取，请重新搜索选择。';}}
  }
 }
 return {activate};
}
root.CampusAgentActions={destination,accepted,markup,bind,restartInput,bindTargets};if(typeof module==='object'&&module.exports)module.exports=root.CampusAgentActions;
})(typeof window==='undefined'?globalThis:window);
