(function(root){
  'use strict';
  const levels={ASSOCIATE:1,BACHELOR:2,MASTER:3,PHD:4};
  const statuses={ENROLLED:'在读',GRADUATED:'已毕业',UNKNOWN:'尚未说明'};
  let sequence=0;
  const newID=()=>root.crypto?.randomUUID?root.crypto.randomUUID().replaceAll('-',''):'education-'+Date.now()+'-'+(++sequence);
  function entries(profile){
    if(Array.isArray(profile.educations))return structuredClone(profile.educations);
    if(profile.degree||profile.graduation_year||profile.majors?.length)return [{id:'legacy-education',degree:profile.degree||'',majors:[...(profile.majors||[])],start_year:0,graduation_year:profile.graduation_year||0,graduation_month:profile.graduation_month||0,status:'UNKNOWN'}];
    return [];
  }
  function recommended(items){
    let index=0;items.forEach((e,i)=>{if((levels[e.degree]||0)>(levels[items[index]?.degree]||0)||((levels[e.degree]||0)===(levels[items[index]?.degree]||0)&&(e.graduation_year||0)>(items[index]?.graduation_year||0)))index=i;});return index;
  }
  function read(data){
    if(data.get('education-count')===null)return null;
    const count=Number(data.get('education-count'));if(!Number.isInteger(count)||count<0||count>8)throw new Error('最多维护 8 段教育经历。');
    const educations=[];
    for(let i=0;i<count;i++)educations.push({id:data.get(`education-${i}-id`)||newID(),degree:data.get(`education-${i}-degree`)||'',majors:root.CampusDisplay.parseList(data.get(`education-${i}-majors`)),start_year:Number(data.get(`education-${i}-start_year`)||0),graduation_year:Number(data.get(`education-${i}-graduation_year`)||0),graduation_month:Number(data.get(`education-${i}-graduation_month`)||0),status:data.get(`education-${i}-status`)||'UNKNOWN'});
    return {educations,primary_education_id:data.get('primary_education_id')||educations[recommended(educations)]?.id||''};
  }
  function project(profile){
    if(!profile.educations?.length)return {...profile,degree:'',graduation_year:0,graduation_month:0,graduation_from:0,graduation_to:0,majors:[],primary_education_id:''};
    const e=profile.educations.find(e=>e.id===profile.primary_education_id)||profile.educations[recommended(profile.educations)];
    return {...profile,degree:e.degree,graduation_year:e.graduation_year,graduation_month:e.graduation_month||0,graduation_from:e.id==='legacy-education'&&!e.graduation_year?profile.graduation_from||0:0,graduation_to:e.id==='legacy-education'&&!e.graduation_year?profile.graduation_to||0:0,majors:[...new Set(profile.educations.flatMap(e=>e.majors||[]))],primary_education_id:e.id};
  }
  function merge(profile,selected,primaryIndex){
    const educations=entries(profile),mapping=new Map();
    const key=e=>JSON.stringify([e.degree,[...(e.majors||[])].map(v=>v.toLowerCase()).sort(),e.graduation_year||0]);
    // A legacy scalar summary may have mixed bachelor/master majors or years.
    // When the reviewed import replaces that degree, do not retain a fictitious
    // third education constructed from the old flattened fields.
    const legacy=educations.findIndex(e=>e.id==='legacy-education');
    if(legacy>=0&&selected.some(({entry})=>entry.degree===educations[legacy].degree)&&!selected.some(({entry})=>key(entry)===key(educations[legacy])))educations.splice(legacy,1);
    for(const {entry,index} of selected){
      const old=educations.find(e=>key(e)===key(entry)&&(!e.start_year||!entry.start_year||e.start_year===entry.start_year));
      if(old){Object.assign(old,entry,{id:old.id,start_year:entry.start_year||old.start_year,graduation_month:entry.graduation_month||old.graduation_month||0,status:entry.status==='UNKNOWN'?old.status:entry.status});mapping.set(index,old.id);}
      else {const item={...entry,id:newID()};educations.push(item);mapping.set(index,item.id);}
    }
    if(educations.length>8)throw new Error('教育经历超过 8 段，请先删除重复项。');
    if(!mapping.has(primaryIndex))throw new Error('请从勾选的教育经历中选择本轮校招使用的一段。');
    return project({...profile,educations,primary_education_id:mapping.get(primaryIndex)});
  }
  function render(items,primary,esc,D){
    const chosen=items.some(e=>e.id===primary)?primary:items[recommended(items)]?.id;
    return `<input type="hidden" name="education-count" value="${items.length}"><div class="section-title"><h3>教育经历</h3><button id="add-education" type="button" class="btn" ${items.length>=8?'disabled':''}>添加教育经历</button></div><p class="form-note">可添加多段教育经历，并选择本轮校招使用的一段。</p>${items.map((e,i)=>`<fieldset class="education-row"><legend>第 ${i+1} 段 · ${esc(D.enums.degree[e.degree]||'教育经历')}</legend><input type="hidden" name="education-${i}-id" value="${esc(e.id)}"><div class="form-grid"><label>学历<select name="education-${i}-degree"><option value="">请选择</option>${Object.entries(D.enums.degree).map(([v,name])=>`<option value="${v}" ${v===e.degree?'selected':''}>${esc(name)}</option>`).join('')}</select></label><label>专业<input name="education-${i}-majors" value="${esc(D.inputList(e.majors))}" maxlength="200"></label><label>入学年份<input name="education-${i}-start_year" type="number" min="1970" max="2100" value="${e.start_year||''}"></label><label>毕业／预计毕业年份<input name="education-${i}-graduation_year" type="number" min="2000" max="2100" value="${e.graduation_year||''}"></label><label>毕业／预计毕业月份（可选）<select name="education-${i}-graduation_month"><option value="0">暂不确定</option>${Array.from({length:12},(_,n)=>`<option value="${n+1}" ${e.graduation_month===n+1?'selected':''}>${n+1} 月</option>`).join('')}</select></label><label>就读状态<select name="education-${i}-status">${Object.entries(statuses).map(([v,name])=>`<option value="${v}" ${v===e.status?'selected':''}>${name}</option>`).join('')}</select></label></div><div class="actions"><label class="check"><input type="radio" name="primary_education_id" value="${esc(e.id)}" ${e.id===chosen?'checked':''}>本轮校招使用这段经历</label><button id="remove-education-${i}" type="button">移除这段</button></div></fieldset>`).join('')||'<p class="empty">暂无教育经历，可手动添加或从简历导入。</p>'}`;
  }
  const api={entries,recommended,read,project,merge,render,newID,statuses};root.CampusProfileEducation=api;if(typeof module==='object'&&module.exports)module.exports=api;
})(globalThis);
