'use strict';
const CampusInventory=(function(root){
  let savedScope='',saved=null;
  function remember(scope,value){savedScope=scope;saved=value;}
  function recall(scope){return savedScope===scope?saved:null;}
  function decode(fields,row){
    if(!Array.isArray(row)||row.length!==fields.length)throw new Error('岗位列表格式暂不可读取，请刷新。');
    const v=Object.fromEntries(fields.map((key,i)=>[key,row[i]]));
    if(typeof v.id!=='string')throw new Error('岗位编号无效，请刷新。');
    return {job:{id:v.id,company:v.company,title:v.title,locations:v.cities,created_at:v.created_at,updated_at:v.updated_at,job_type:v.job_type,current_status:v.current_status},cities:v.cities,input_key:v.input_key,state:v.state,analysis_mode:v.analysis_mode,excluded_reason:v.excluded_reason,disposition:v.disposition,preliminary_score:v.preliminary_score,local:{score:v.preliminary_score,role:v.role,tier:v.tier,direction:{status:v.direction}},score:v.score,priority:v.priority,holistic:v.fit?{fit:v.fit}:null,application:v.application,campaign:v.campaign,text_bytes:v.text_bytes,company_placement:v.company_placement,coverage:v.coverage,card_pending:true};
  }
  function merge(previous,response){
    const index=response.index;if(!index||!Array.isArray(index.fields))throw new Error('岗位列表暂不可读取，请刷新。');
    const changed=previous?.snapshot_key!==response.snapshot_key;
    const rows=new Map(index.full?[]:(previous?.jobs||[]).map(r=>[r.job.id,changed?{...r,card_pending:true}:r]));
    for(const id of index.removed||[])rows.delete(id);
    for(const values of (index.full?index.rows:index.upserts)||[]){const r=decode(index.fields,values);rows.set(r.job.id,r);}
    for(const r of response.jobs||[])if(rows.has(r.job.id))rows.set(r.job.id,r);
    return {...response,jobs:[...rows.values()]};
  }
  const api={merge,decode,remember,recall};if(typeof module==='object'&&module.exports)module.exports=api;root.CampusInventory=api;return api;
})(typeof window==='undefined'?globalThis:window);
