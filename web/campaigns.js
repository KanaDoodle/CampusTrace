'use strict';
const CampusCampaigns=(function(root){
 function renderRules(rules,{esc}){return rules.length?`<div class="campaign-rules">${rules.map(r=>`<article class="campaign-rule"><div><strong>${esc(r.company)} · ${esc(r.name)}</strong><p class="meta">${r.job_ids.length} 个岗位共用 ${r.limit} 个名额 · 计划 ${r.planned} 个 · 已投递 ${r.submitted} 个 · 剩余 ${r.remaining} 个</p>${r.conflict?'<p class="pending-note">现有记录超过这条规则的名额，请核对官网规则与已有计划。</p>':''}${r.rule_url?`<a href="${esc(safeURL(r.rule_url))}" target="_blank" rel="noopener noreferrer">查看已记录的规则来源</a>`:''}</div><div class="actions"><button class="btn btn-small" data-campaign-edit="${esc(r.id)}">修改规则</button><button class="btn btn-small" data-campaign-delete="${esc(r.id)}">移除规则</button></div></article>`).join('')}</div>`:'<p class="meta">还没有限投规则。请核对招聘官网后，选择共用名额的岗位。</p>';}
 function safeURL(v){try{const u=new URL(v);return ['http:','https:'].includes(u.protocol)&&!u.username&&!u.password?u.href:'';}catch{return '';}}
 async function mount(box,{api,esc,active=()=>true,notify}){
  const [initial,catalog]=await Promise.all([api('/api/application-campaigns'),api('/api/application-campaigns/catalog')]);if(!active()||!box.isConnected)return;
  let rules=initial,edit=null,company='',query='',selected=new Set(),formOpen=false,confirmDelete='',busy=false,confirmed=false,baseline='';
  const companies=[...new Map(catalog.map(j=>[j.company_id,j.company])).entries()].sort((a,b)=>a[1].localeCompare(b[1],'zh'));
  const q=s=>box.querySelector(s);
  function snapshot(){return formOpen?JSON.stringify({company,name:q('#campaign-name')?.value||'',limit:q('#campaign-limit')?.value||'1',url:q('#campaign-url')?.value||'',selected:[...selected].sort(),confirmed:!!q('#campaign-confirmed')?.checked}):'';}
  const dirty=()=>busy||(formOpen&&snapshot()!==baseline);
  const discard=async()=>!dirty()||await root.CampusNavigation.confirmDiscard();
  function draw(){
   const assigned=new Set(rules.filter(r=>r.id!==edit?.id).flatMap(r=>r.job_ids)),matches=catalog.filter(j=>j.company_id===company&&(!query||(j.title+' '+j.locations.join(' ')).toLowerCase().includes(query.toLowerCase()))),shown=matches.slice(0,200);
   box.innerHTML=`<details class="campaign-panel" ${formOpen||confirmDelete?'open':''}><summary>同公司限投规则 <span class="meta">${rules.length} 条</span></summary><div class="campaign-body"><p class="form-note">按招聘批次选择共用名额的岗位。加入投递计划时占用名额；尚未投递的计划撤回后释放。已经投递的记录保留占用，即使后来拒绝或撤回也不自动释放。规则由你根据官网确认。</p>${renderRules(rules,{esc})}${confirmDelete?`<div class="note-box"><p>移除这条规则后，将停止检查它的名额；投递记录会保留。</p><button id="campaign-delete-confirm" class="btn">确认移除</button><button id="campaign-delete-cancel" class="btn">取消</button></div>`:''}<button id="campaign-add" class="btn btn-small" ${busy?'disabled':''}>添加限投规则</button>${formOpen?`<form id="campaign-form"><div class="form-grid"><label>公司<select id="campaign-company" name="company_id" required><option value="">请选择公司</option>${companies.map(([id,name])=>`<option value="${esc(id)}" ${id===company?'selected':''}>${esc(name)}</option>`).join('')}</select></label><label>招聘批次／规则名称<input id="campaign-name" name="name" maxlength="60" required value="${esc(edit?.name||'')}" placeholder="例如：2027 校招 · 研发岗位"></label><label>共用投递名额<input name="limit" id="campaign-limit" type="number" min="1" max="10" value="${edit?.limit||1}" required></label><label>规则来源（可选）<input id="campaign-url" name="rule_url" type="url" maxlength="2048" value="${esc(edit?.rule_url||'')}" placeholder="招聘官网规则说明网址"></label></div><label>筛选要纳入的岗位<input id="campaign-search" type="search" value="${esc(query)}" placeholder="按岗位名称或城市搜索"></label><p class="meta"><span id="campaign-selected-count">已选 ${selected.size} / 200 个</span>。只检查明确勾选的岗位，其他岗位不受这条规则影响。${matches.length>200?'当前展示前 200 个，请输入关键词缩小范围。':''}</p><div class="campaign-job-list">${shown.map(j=>`<label class="check"><input type="checkbox" data-campaign-job="${esc(j.id)}" ${selected.has(j.id)?'checked':''} ${assigned.has(j.id)?'disabled':''}>${esc(j.title)} <small>${esc(j.locations.join(' / '))}${assigned.has(j.id)?' · 已属于另一条规则':''}</small></label>`).join('')||'<p class="meta">请先选择公司，或调整搜索条件。</p>'}</div><label class="check"><input name="confirmed" type="checkbox" id="campaign-confirmed" required ${confirmed?'checked':''}>我已核对招聘官网，确认这些岗位共用上述名额</label><div class="actions"><button class="btn btn-primary" ${busy||!selected.size?'disabled':''}>保存规则</button><button id="campaign-form-cancel" type="button" class="btn">取消</button></div></form>`:''}</div></details>`;
   function keepDraft(){if(!formOpen)return;edit={...edit,name:q('#campaign-name').value,limit:Number(q('#campaign-limit').value),rule_url:q('#campaign-url').value};confirmed=q('#campaign-confirmed').checked;}
   q('#campaign-add').onclick=async()=>{if(busy||!await discard())return;edit=null;company='';selected.clear();query='';formOpen=true;confirmed=false;confirmDelete='';draw();baseline=snapshot();};
   for(const b of box.querySelectorAll('[data-campaign-edit]'))b.onclick=async()=>{if(busy||!await discard())return;edit=rules.find(r=>r.id===b.dataset.campaignEdit);company=edit.company_id;selected=new Set(edit.job_ids);query='';formOpen=true;confirmed=false;draw();baseline=snapshot();};
   for(const b of box.querySelectorAll('[data-campaign-delete]'))b.onclick=async()=>{if(busy||!await discard())return;confirmDelete=b.dataset.campaignDelete;formOpen=false;draw();};
   if(confirmDelete){q('#campaign-delete-cancel').onclick=()=>{confirmDelete='';draw();};q('#campaign-delete-confirm').onclick=async()=>{try{const r=rules.find(v=>v.id===confirmDelete);await api('/api/application-campaigns/'+encodeURIComponent(r.id),'DELETE',{version:r.version});rules=await api('/api/application-campaigns');confirmDelete='';if(active())draw();notify('限投规则已移除。');}catch(e){notify(e.message);}};}
   if(!formOpen)return;
   q('#campaign-company').onchange=e=>{keepDraft();company=e.target.value;selected.clear();query='';confirmed=false;draw();};
   q('#campaign-search').onchange=e=>{keepDraft();query=e.target.value.trim();draw();};
   for(const b of box.querySelectorAll('[data-campaign-job]'))b.onchange=()=>{if(b.checked&&selected.size>=200){b.checked=false;notify('每条规则最多选择 200 个岗位，请缩小批次范围。');return;}b.checked?selected.add(b.dataset.campaignJob):selected.delete(b.dataset.campaignJob);confirmed=false;q('#campaign-confirmed').checked=false;q('#campaign-form button[type="submit"], #campaign-form .btn-primary').disabled=!selected.size;q('#campaign-selected-count').textContent='已选 '+selected.size+' / 200 个';};
   q('#campaign-form-cancel').onclick=async()=>{if(busy||!await discard())return;formOpen=false;edit=null;draw();};
   q('#campaign-form').onsubmit=async e=>{
    e.preventDefault();if(busy)return;const form=new FormData(e.target);busy=true;
    const controls=[...e.target.elements].map(el=>[el,el.disabled]);for(const [el] of controls)el.disabled=true;
    let committed=false;
    try{
     const body={company_id:company,name:form.get('name'),limit:Number(form.get('limit')),rule_url:String(form.get('rule_url')||'').trim(),confirmed:form.has('confirmed'),job_ids:[...selected],version:edit?.version||0};
     await api('/api/application-campaigns'+(edit?.id?'/'+encodeURIComponent(edit.id):''),edit?.id?'PUT':'POST',body);committed=true;
     const latest=await api('/api/application-campaigns');if(!active())return;rules=latest;formOpen=false;edit=null;busy=false;draw();notify('限投规则已保存。');
    }catch(error){if(active()){if(committed){formOpen=false;edit=null;busy=false;draw();notify('规则已保存，列表刷新失败。请重新进入投递进展查看最新规则。');}else notify(error.message);}}
    finally{busy=false;for(const [el,disabled] of controls)if(el.isConnected)el.disabled=disabled;}
   };

  }
  draw();return {dirty};
 }
 const api={mount,renderRules,safeURL};if(typeof module==='object'&&module.exports)module.exports=api;root.CampusCampaigns=api;return api;
})(typeof window==='undefined'?globalThis:window);
