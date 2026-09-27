'use strict';
const CampusProfile=(function(){
  async function page(set,heading,{api,esc,D,formAction,UserError,navigate}) {
    const readProfile=async()=>{try{return await api('/api/profile');}catch(error){if(error.status===404)return {revision:0};throw error;}};
    const [profile,projects,facts,capabilities]=await Promise.all([readProfile(),api('/api/projects'),api('/api/project_facts'),api('/api/profile/resume/capabilities')]);
    CampusModels.bindUser(capabilities.user_id);
    const state={profile,projects,facts,capabilities,preview:'',draft:null};
    const notice=message=>{document.querySelector('#notice').textContent=message;};
    const listFields=['majors','preferred_cities','acceptable_cities','target_roles','technical_skills','target_languages'];
    const modelFields=new Set(['graduation_year','degree','majors','technical_skills','target_languages','experience_months','target_roles']);
    const currentValue=field=>Array.isArray(state.profile[field])?state.profile[field].join('、'):(state.profile[field]??'尚未填写');
    const factKind=(selected='IMPLEMENTED')=>Object.entries(D.enums.fact).map(([v,label])=>`<option value="${v}" ${selected===v?'selected':''}>${esc(label)}</option>`).join('');
    const projectOptions=(selected='')=>state.projects.map(p=>`<option value="${esc(p.id)}" ${p.id===selected?'selected':''}>${esc(p.name)}</option>`).join('');
    const numeric=(value,label,min,max)=>{const n=Number(value);if(!Number.isInteger(n)||n<min||n>max)throw new UserError(`${label}应在 ${min}—${max} 之间。`);return n;};
    const render=()=>{
      const p=state.profile;
      const input=(name,label,value='',type='text',extra='')=>`<label>${esc(label)}<input name="${esc(name)}" type="${type}" value="${esc(value)}" ${extra}></label>`;
      const area=(name,label,value='',extra='')=>`<label>${esc(label)}<textarea name="${esc(name)}" ${extra}>${esc(value)}</textarea></label>`;
      const profileForm=`<form id="save-profile" novalidate><div class="form-grid">${input('graduation_year','毕业届别（年份）',p.graduation_year||'','number','min="2000" max="2100"')}${input('graduation_from','毕业范围起点',p.graduation_from||'','number','min="2000" max="2100"')}${input('graduation_to','毕业范围终点',p.graduation_to||'','number','min="2000" max="2100"')}<label>最高学历<select name="degree"><option value="">请选择</option>${Object.entries(D.enums.degree).map(([v,label])=>`<option value="${v}" ${p.degree===v?'selected':''}>${esc(label)}</option>`).join('')}</select></label>${input('experience_months','相关实习或工作经验（月）',p.experience_months??0,'number','min="0"')}</div><fieldset><legend>意向岗位类型</legend>${Object.entries(D.enums.job_type).filter(([v])=>v!=='UNKNOWN').map(([v,label])=>`<label class="check"><input name="preferred_job_types" type="checkbox" value="${v}" ${(p.preferred_job_types||[]).includes(v)?'checked':''}>${esc(label)}</label>`).join('')}</fieldset><p class="meta">多项内容可用顿号或逗号分隔；只填写与岗位匹配有关的信息，姓名和联系方式留在本地简历中。</p><div class="form-grid">${listFields.map(k=>input(k,D.field(k),D.inputList(p[k]))).join('')}</div><button>保存求职资料</button></form>`;
      const projectCards=state.projects.map(project=>{
        const items=state.facts.filter(f=>f.project_id===project.id);
        return `<article class="card"><h4>${esc(project.name)}</h4><details><summary>修改项目名称</summary><form data-edit-project="${esc(project.id)}"><label>项目名称<input name="name" value="${esc(project.name)}" maxlength="200" required></label><button>保存名称</button></form></details>${items.length?items.map(f=>`<details class="fact-row"><summary>${esc(D.label('fact',f.kind))} · ${f.verified?'我已核对':'待核对'} · ${esc(f.claim)}</summary><form data-edit-fact="${esc(f.id)}"><input type="hidden" name="project_id" value="${esc(project.id)}"><label>事实类型<select name="kind">${factKind(f.kind)}</select></label>${area('claim','事实内容',f.claim,'required maxlength="4000"')}${area('reference','依据或简历摘录',f.reference||'','maxlength="1000"')}<label class="check"><input type="checkbox" name="verified" ${f.verified?'checked':''}>我已核对这条事实；只有已实现且已核对的内容可作为项目成果</label><button>保存修改</button></form></details>`).join(''):'<p class="meta">暂无项目事实。</p>'}</article>`;
      }).join('');
      const draftSuggestions=state.draft?.suggestions?.length?`<form id="apply-suggestions"><h4>简历提取的资料建议</h4><p class="meta">勾选并可改写每项；未勾选的不会保存。已有资料会显示在左侧。</p>${state.draft.suggestions.map((s,i)=>`<div class="draft-row"><label class="check"><input type="checkbox" name="pick" value="${i}">${esc(D.field(s.field))}</label><span>现有：${esc(currentValue(s.field))}</span><label>建议值<input name="value-${i}" value="${esc(s.value)}" maxlength="120"></label><small>简历依据：${esc(s.excerpt)}</small></div>`).join('')}<button>保存勾选的资料</button></form>`:'';
      const draftProjects=state.draft?.projects?.map((project,i)=>`<form class="draft-project" data-draft-project="${i}"><h4>项目草稿 ${i+1}</h4><p class="meta">来源摘录：${esc(project.excerpt)}</p><label>项目名称<input name="name" value="${esc(project.name)}" maxlength="200" required></label><label>保存到<select name="existing"><option value="">新建项目</option>${projectOptions(project.savedProjectID)}</select></label>${project.facts.map((fact,j)=>`<fieldset><legend>项目事实 ${j+1}</legend><label class="check"><input type="checkbox" name="fact-${j}">保存这条事实</label><label>类型<select name="kind-${j}">${factKind(fact.kind)}</select></label>${area('claim-'+j,'事实内容',fact.claim,'maxlength="4000"')}<small>简历依据：${esc(fact.excerpt)}</small><label class="check"><input type="checkbox" name="verified-${j}">我已逐字核对并确认这条事实真实</label></fieldset>`).join('')}<button>保存这个项目中勾选的事实</button></form>`).join('')||'';
      const html=`${heading}<p>在这里维护求职条件和项目经历。匹配与问答使用已保存的字段；模型提取只提供可核对的草稿。</p><h3>求职条件</h3>${profileForm}<h3>项目经历与事实</h3><p class="meta">项目事实可以手动添加和修改。计划与局限请分别标记；未核对的草稿不会作为已完成成果。</p><form id="add-project"><label>新项目名称<input name="name" maxlength="200" required placeholder="例如：课程项目／个人项目"></label><button>添加项目</button></form>${projectCards||'<p class="empty">暂无项目，可先添加一个。</p>'}<form id="add-fact"><h4>手动添加项目事实</h4><label>所属项目<select name="project_id" required><option value="">请选择</option>${projectOptions()}</select></label><label>类型<select name="kind">${factKind()}</select></label>${area('claim','事实内容','','required maxlength="4000"')}${area('reference','依据（可选；不要填写姓名、联系方式）','','maxlength="1000"')}<label class="check"><input type="checkbox" name="verified">我已核对这条事实真实</label><button ${state.projects.length?'':'disabled'}>添加事实</button></form><h3>从简历生成草稿</h3><p>PDF、DOCX、TXT 在浏览器本地读取。原文件和原始文字不会上传。扫描版 PDF 暂不支持。</p><label>选择简历（不超过 5 MB）<input id="resume-file" type="file" accept=".pdf,.docx,.txt,application/pdf,text/plain,application/vnd.openxmlformats-officedocument.wordprocessingml.document"></label><p class="meta">自动遮盖带标签的姓名、常见手机格式、邮箱、证件号、地址标签和链接。无标签姓名可在下方手动指定遮盖；仍请逐行检查，只保留与匹配有关的非敏感事实。</p><label>即将发送给外部模型的完整文字<textarea id="resume-preview" maxlength="16000" placeholder="选择简历后显示脱敏预览；也可直接粘贴已脱敏文字。">${esc(state.preview)}</textarea></label><div class="actions"><label>额外遮盖的姓名或称呼（仅本地使用）<input id="mask-name" maxlength="60" autocomplete="off" placeholder="例如：张三"></label><button id="mask-name-button" type="button">在预览中遮盖</button></div><label class="check"><input id="privacy-check" type="checkbox">我已检查上方完整文字，确认可以发送给外部模型</label><button id="analyze-resume" type="button" ${CampusModels.available(state.capabilities)?'':'disabled'}>让模型生成草稿</button><button id="resume-model-settings" type="button">选择外部模型</button><small>${CampusModels.available(state.capabilities)?`当前模型：${esc(CampusModels.label(state.capabilities))}。只有点击后才发送上方文字。`:'当前未配置外部模型；仍可手动维护求职资料和项目事实。'}</small>${state.draft?`<div id="resume-draft"><h3>待确认草稿</h3>${draftSuggestions}${draftProjects||'<p class="meta">没有提取到项目事实。</p>'}</div>`:''}`;
      if(!set(html))return;
      bind();
    };
    const bind=()=>{
      formAction('#save-profile',async data=>{
        const body={...state.profile};
        for(const k of ['graduation_year','graduation_from','graduation_to','experience_months'])body[k]=Number(data.get(k)||0);
        body.degree=data.get('degree');body.preferred_job_types=data.getAll('preferred_job_types');
        for(const k of listFields)body[k]=D.parseList(data.get(k));
        await api('/api/profile','PUT',body);state.profile=await readProfile();render();notice('求职资料已保存。');
      });
      formAction('#add-project',async data=>{const project=await api('/api/projects','POST',{name:String(data.get('name')).trim()});state.projects.push(project);render();notice('项目已添加。');});
      for(const form of document.querySelectorAll('[data-edit-project]'))formAction(`[data-edit-project="${form.dataset.editProject}"]`,async data=>{const id=form.dataset.editProject;const project=await api('/api/projects/'+encodeURIComponent(id),'PUT',{name:String(data.get('name')).trim()});state.projects=state.projects.map(old=>old.id===id?project:old);render();notice('项目名称已更新。');});
      formAction('#add-fact',async data=>{const fact=await api('/api/project_facts','POST',{project_id:data.get('project_id'),kind:data.get('kind'),claim:String(data.get('claim')).trim(),reference:String(data.get('reference')).trim(),verified:data.has('verified')});state.facts.push(fact);render();notice('项目事实已添加。');});
      for(const form of document.querySelectorAll('[data-edit-fact]'))formAction(`[data-edit-fact="${form.dataset.editFact}"]`,async data=>{const id=form.dataset.editFact;const fact=await api('/api/project_facts/'+encodeURIComponent(id),'PUT',{project_id:data.get('project_id'),kind:data.get('kind'),claim:String(data.get('claim')).trim(),reference:String(data.get('reference')).trim(),verified:data.has('verified')});state.facts=state.facts.map(old=>old.id===id?fact:old);render();notice('项目事实已更新。');});
      const preview=document.querySelector('#resume-preview');preview.oninput=()=>{state.preview=preview.value;state.draft=null;document.querySelector('#resume-draft')?.remove();};
      document.querySelector('#resume-model-settings').onclick=()=>navigate('models');
      document.querySelector('#mask-name-button').onclick=()=>{const field=document.querySelector('#mask-name');try{const masked=CampusProfileLocal.maskAdditionalName(preview.value,field.value);if(masked===preview.value){notice('预览中没有找到这个姓名或称呼。');return;}preview.value=masked;state.preview=masked;state.draft=null;document.querySelector('#resume-draft')?.remove();document.querySelector('#privacy-check').checked=false;notice('已在本地遮盖该姓名或称呼；请继续检查外发文字。');}catch(error){notice(error.message);}finally{field.value='';}};
      document.querySelector('#resume-file').onchange=async event=>{const file=event.target.files[0];if(!file)return;state.preview='';state.draft=null;preview.value='';document.querySelector('#resume-draft')?.remove();document.querySelector('#privacy-check').checked=false;try{state.preview=await CampusProfileLocal.readFile(file);render();notice('简历已在本地读取并初步遮盖，请逐行核对外发文字。');}catch(error){notice(error.message||'无法读取简历。');}finally{event.target.value='';}};
      document.querySelector('#analyze-resume').onclick=async()=>{
        const button=document.querySelector('#analyze-resume');
        state.preview=preview.value;
        if(!document.querySelector('#privacy-check').checked){notice('请先检查完整外发文字并勾选确认。');return;}
        if(!state.preview.trim()||state.preview.length>16000){notice('请保留不超过 16000 字的已脱敏文字。');return;}
        if(CampusProfileLocal.hasDirectIdentifiers(state.preview)){notice('文字里仍有电话、邮箱、链接、证件号或身份标签，请先移除。');return;}
        button.disabled=true;notice('正在生成草稿，尚未保存任何资料…');
        try{const sent=state.preview;const draft=await api('/api/profile/resume/draft','POST',{text:sent,model_config:CampusModels.requestConfig()});if(state.preview!==sent){notice('外发文字已修改，本次草稿已丢弃；请重新生成。');return;}state.draft=draft;render();notice('草稿已生成。请逐项核对并勾选需要保存的内容。');}catch(error){notice(error.message||'草稿生成失败。');}finally{button.disabled=false;}
      };
      if(state.draft?.suggestions?.length)formAction('#apply-suggestions',async(data,form)=>{
        const selected=new Set(data.getAll('pick').map(Number));if(!selected.size)throw new UserError('请先勾选要保存的建议。');
        const latest=await readProfile();if(latest.revision!==state.profile.revision){state.profile=latest;render();throw new UserError('求职资料已在别处更新，请重新核对现有值。');}
        const body={...latest};
        for(const [i,s] of state.draft.suggestions.entries())if(selected.has(i)){
          if(!modelFields.has(s.field))throw new UserError('草稿包含未知字段。');
          const value=String(data.get('value-'+i)||'').trim();if(!value)throw new UserError('勾选项的建议值不能为空。');
          if(s.field==='graduation_year')body[s.field]=numeric(value,'毕业届别',2000,2100);
          else if(s.field==='experience_months')body[s.field]=numeric(value,'经验月数',0,600);
          else if(s.field==='degree'){if(!Object.hasOwn(D.enums.degree,value))throw new UserError('请选择有效学历代码。');body.degree=value;}
          else {const values=body[s.field]||[];if(!values.some(v=>v.toLowerCase()===value.toLowerCase()))body[s.field]=[...values,value];}
        }
        await api('/api/profile','PUT',body);state.profile=await readProfile();state.draft.suggestions=state.draft.suggestions.filter((_,i)=>!selected.has(i));render();notice('勾选的资料已保存。');
      });
      for(const form of document.querySelectorAll('[data-draft-project]'))formAction(`[data-draft-project="${form.dataset.draftProject}"]`,async data=>{
        const i=Number(form.dataset.draftProject),draft=state.draft.projects[i];
        let project=state.projects.find(p=>p.id===(draft.savedProjectID||data.get('existing')));
        if(!project){const name=String(data.get('name')||'').trim();project=state.projects.find(p=>p.name.toLowerCase()===name.toLowerCase());}
        if(!project){project=await api('/api/projects','POST',{name:String(data.get('name')).trim()});state.projects.push(project);}
        draft.savedProjectID=project.id;
        let saved=0;
        for(const [j,f] of draft.facts.entries())if(data.has('fact-'+j)){
          const claim=String(data.get('claim-'+j)||'').trim();if(!claim)throw new UserError('勾选的项目事实不能为空。');
          if(state.facts.some(old=>old.project_id===project.id&&old.claim===claim&&old.kind===data.get('kind-'+j)))continue;
          const fact=await api('/api/project_facts','POST',{project_id:project.id,kind:data.get('kind-'+j),claim,reference:f.excerpt,verified:data.has('verified-'+j)});
          state.facts.push(fact);saved++;
        }
        state.draft.projects.splice(i,1);render();notice(`项目已保存，新增 ${saved} 条事实。`);
      });
    };
    render();
  }
  return {page};
})();
