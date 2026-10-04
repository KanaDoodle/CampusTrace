'use strict';
const CampusEvidence=(function(root){
  function claimFrom(values){return [['work','实际工作'],['method','实现方法'],['outcome','结果与范围']].map(([k,label])=>String(values[k]||'').trim()?label+'：'+String(values[k]).trim():'').filter(Boolean).join('\n');}
  function requirementFor(v,id){const r=v.result?.requirements?.find(r=>r.id===id),m=v.result?.matches?.find(m=>m.requirement_id===id);return v.state==='ANALYZED'&&v.result?.input_key&&r&&r.category!=='QUALIFICATION'&&m?.result==='NO_EVIDENCE'?r:null;}
  async function page(set,heading,{api,esc,D,formAction,UserError,navigate,active=()=>true},target){
    const identity={model_url:target.identity?.model_url||'',model_name:target.identity?.model_name||'',mask_name:target.identity?.mask_name||''};
    const resultPath='/api/matching/results/'+encodeURIComponent(target.jobID);
    const [v,projects,facts,record]=await Promise.all([api(resultPath,'POST',identity),api('/api/projects'),api('/api/project_facts'),api('/api/jobs/'+encodeURIComponent(target.jobID))]);
    if(!active())return;
    const drafts=root.CampusNavigation?.forms(document);
    if(!active())return;
    const req=requirementFor(v,target.requirementID),inputKey=v.result?.input_key;
    let saved=false,createdProject='',busy=false;
    const go=(...args)=>Promise.resolve(navigate(...args)).catch(error=>root.CampusUI.notify(error.message));
    const returnJob=()=>go('matching',{jobID:target.jobID,view:'evidence'});
    const render=()=>{
      const job=record.job||{};
      const context=`<aside class="evidence-context"><p class="form-note">${esc(job.company)}</p><h3>${esc(job.title)}</h3>${req?`<h4>这次要核对的要求</h4><p>${esc(req.text)}</p><blockquote>${esc(req.excerpt)}</blockquote><p class="form-note">岗位原文说明招聘要求，不能作为个人经历的证明。</p>`:''}<button class="btn btn-small" id="evidence-return">返回岗位依据</button></aside>`;
      const body=!req?'<p class="pending-note">岗位或资料已变化，或此项不再需要补充依据。请返回岗位查看当前分析，更新后再核对。</p>':saved?`<h3>项目事实已保存并确认</h3><p>这条事实已加入求职资料。已有分析需要更新后，才能判断它支持哪些岗位要求。</p><p class="form-note">其他岗位也会按当前资料标为待更新；不会自动调用模型。</p><div class="actions"><button class="btn btn-primary" id="evidence-analyze">核对外发资料并更新本岗位分析</button><button class="btn" id="evidence-profile">查看求职资料</button></div>`:`<form id="evidence-form"><h3>补充真实的项目经历</h3><p class="form-note">填写你已经完成的工作。不确定的结果可以留空，不需要为了匹配岗位补写经历。</p><label>所属项目<select id="evidence-project" name="project_id"><option value="">新建项目</option>${projects.map(p=>`<option value="${esc(p.id)}" ${p.id===(createdProject||projects[0]?.id)?'selected':''}>${esc(p.name)}</option>`).join('')}</select></label><label id="evidence-new-project" ${projects.length?'hidden':''}>新项目名称<input name="project_name" maxlength="60" ${projects.length?'':'required'} placeholder="例如：课程任务调度系统"></label><label>我实际负责并完成的工作<textarea name="work" maxlength="500" required placeholder="说明你亲自完成了什么，不要直接复制岗位要求"></textarea></label><label>实现方法（可选）<textarea name="method" maxlength="500" placeholder="使用了哪些技术或机制？你做了哪些具体实现？"></textarea></label><label>结果与适用范围（可选）<textarea name="outcome" maxlength="200" placeholder="写可核对的结果与范围；未测试过的规模或指标不要填写"></textarea></label><label>个人依据或记录位置（可选）<textarea name="reference" maxlength="250" placeholder="例如：项目文档中的章节；不要填写联系方式或密钥"></textarea></label><details><summary>核对将保存的事实文字</summary><pre id="evidence-preview" class="evidence-preview">填写后在这里核对</pre></details><label class="check"><input name="verified" type="checkbox" required>我确认这些是本人已完成的真实工作，已核对内容；计划和未经验证的指标不写成成果</label><button class="btn btn-primary" type="submit">保存并确认项目事实</button></form>`;
      if(!set(root.CampusUI.heading('补充项目依据','对照岗位要求，把真实经历补充到求职资料。')+`<div class="evidence-layout">${context}<section class="evidence-form-panel">${body}</section></div>`))return;
      drafts?.restore();root.CampusNavigation?.register({active,dirty:()=>drafts?.dirty()});
      document.querySelector('#evidence-return').onclick=returnJob;
      if(saved){document.querySelector('#evidence-profile').onclick=()=>go('profile');document.querySelector('#evidence-analyze').onclick=()=>go('matching',{jobID:target.jobID,analyze:true});return;}
      if(!req)return;
      const form=document.querySelector('#evidence-form'),select=document.querySelector('#evidence-project');
      select.onchange=()=>{const label=document.querySelector('#evidence-new-project');label.hidden=!!select.value;label.querySelector('input').required=!select.value;};
      form.oninput=()=>{const data=new FormData(form);document.querySelector('#evidence-preview').textContent=claimFrom(Object.fromEntries(data))||'填写后在这里核对';};
      formAction('#evidence-form',async data=>{
        if(busy)return;
        if(!data.has('verified'))throw new UserError('请先逐项核对并确认事实真实。');
        const claim=claimFrom(Object.fromEntries(data));
        if(!String(data.get('work')||'').trim()||new TextEncoder().encode(claim).length>4000)throw new UserError('请填写实际工作，并将事实文字控制在 4000 字节以内。');
        if(root.CampusProfileLocal.hasDirectIdentifiers(claim+'\n'+String(data.get('reference')||'')+'\n'+String(data.get('project_name')||'')))throw new UserError('请移除事实、依据或项目名称中的联系方式、身份信息和链接。');
        busy=true;
        try{
          const current=await api(resultPath,'POST',identity);
          if(!active())return;
          if(current.result?.input_key!==inputKey||!requirementFor(current,target.requirementID))throw new UserError('岗位或资料已变化，请返回岗位更新分析后，再核对要补充的内容。');
          let projectID=String(data.get('project_id')||'');
          if(!projectID){
            const name=String(data.get('project_name')||'').trim();if(!name)throw new UserError('请填写新项目名称。');
            const existing=projects.find(p=>p.name===name);
            if(existing)projectID=existing.id;
            else {const p=await api('/api/projects','POST',{name});projects.push(p);createdProject=p.id;projectID=p.id;select.innerHTML+=`<option value="${esc(p.id)}">${esc(p.name)}</option>`;select.value=p.id;select.onchange();}
          }
          if(!projects.some(p=>p.id===projectID))throw new UserError('请选择属于你的项目。');
          const old=facts.find(f=>f.project_id===projectID&&f.kind==='IMPLEMENTED'&&f.claim===claim);
          const body={project_id:projectID,kind:'IMPLEMENTED',claim,reference:String(data.get('reference')||old?.reference||'').trim(),verified:true};
          if(!old?.verified)await api('/api/project_facts'+(old?'/'+encodeURIComponent(old.id):''),old?'PUT':'POST',body);
          if(!active())return;saved=true;render();root.CampusUI.notify('项目事实已保存。更新分析前，请重新核对外发资料。');
        }finally{busy=false;}
      });
    };
    render();
  }
  const api={page,claimFrom,requirementFor};if(typeof module==='object'&&module.exports)module.exports=api;return api;
})(typeof window==='undefined'?globalThis:window);
