(function(root){
 'use strict';
 const labels={PENDING:'等待开始',PREPARING:'准备来源',QUEUED:'等待读取',FETCHING:'导入岗位',RETRYING:'自动重试中',SUCCESS:'已完成',EMPTY:'暂无岗位',FAILED:'需要重试'};
 const errors={SOURCE_IMPORT_NETWORK:'连接超时或网络异常',SOURCE_IMPORT_BLOCKED:'官网限制了访问',SOURCE_IMPORT_BUSY:'官网暂时繁忙',SOURCE_IMPORT_CAPACITY:'岗位数超过单来源上限，可先到官网查看',SOURCE_IMPORT_CHANGED:'官网返回格式或招聘范围有变化',SOURCE_IMPORT_FAILED:'暂时无法读取来源',SOURCE_IMPORT_DISCOVERY_FAILED:'暂时无法读取完整岗位列表',SOURCE_IMPORT_PARTIAL_FAILURE:'部分岗位未能读取，已保留成功项',SOURCE_IMPORT_INTERRUPTED:'恢复副本中的导入已中断，可重新开始'};
 function mount(host,{api,esc,D,active=()=>true,navigate,count=0}){
  if(!host)return ()=>{};
  let batch=null,busy=false,stopped=false,timer=null,loading=true,warning='',opened=false,revision=0;
  const alive=()=>!stopped&&active();
  const clear=()=>{if(timer!==null){clearTimeout(timer);timer=null;}};
  function draw(){
   if(!alive())return;
   const running=batch?.state==='RUNNING',items=batch?.items||[],done=Number(batch?.completed||0),failed=Number(batch?.failed||0),total=items.length;
   const actionDisabled=busy||loading||count===0;
   host.innerHTML=`<section class="source-bulk" aria-labelledby="bulk-source-title"><div class="source-bulk-heading"><div><h3 id="bulk-source-title">把校招岗位一次收齐</h3><p class="meta">导入全部 ${count} 个已适配来源的当前招聘范围。后台分批读取，离开页面也会继续；周期关注可另行设置。</p></div><button class="btn btn-primary" data-import-all ${actionDisabled?'disabled':''}>${busy?'提交中…':running?'查看当前导入':'一键导入全部岗位'}</button></div><p class="meta">已有岗位会按来源去重，保留关注设置。本次导入不发起大模型深度分析。</p>${warning?`<p class="pending-note" role="status">${esc(warning)} <button class="btn btn-small" data-import-refresh>刷新进度</button></p>`:''}${batch?`<div class="source-bulk-progress" role="status"><b>${running?'正在后台导入':failed?'本轮导入结束，有来源需要重试':'本轮导入完成'}</b><span>来源 ${done}/${total} · 已读取 ${Number(batch.imported||0)} 个岗位${failed?` · 失败 ${failed} 个来源`:''}</span><progress max="${total||1}" value="${done}" aria-label="招聘来源导入进度"></progress></div><div class="actions">${failed?`<button class="btn" data-import-retry ${busy?'disabled':''}>重试失败来源（${failed}）</button>`:''}<button class="btn" data-import-jobs>去岗位雷达初筛</button><button class="btn btn-small" data-import-refresh ${busy?'disabled':''}>刷新进度</button></div><details class="source-bulk-details" ${opened?'open':''}><summary>各来源进度 <span class="meta">${esc(D.date(batch.created_at))}</span></summary><div class="source-bulk-list">${items.map(v=>`<div class="source-bulk-row"><div><b>${esc(v.company)}</b><small>${esc(v.scope)}</small>${v.code&&v.state==='FAILED'?`<small class="pending-note">${esc(errors[v.code]||errors.SOURCE_IMPORT_FAILED)}</small>`:''}</div><div><span class="source-bulk-status ${v.state==='FAILED'?'is-failed':''}">${esc(labels[v.state]||'等待更新')}</span><small>${v.expected?`${Number(v.completed||0)}/${Number(v.expected)} 个岗位${v.failed?`，失败 ${Number(v.failed)}`:''}`:''}</small>${v.source_id?`<button class="btn btn-small" data-import-source="${esc(v.source_id)}">查看岗位</button>`:''}</div></div>`).join('')}</div></details>`:loading?'<p class="meta">正在读取导入进度…</p>':''}</section>`;
   const bind=(selector,fn)=>{const el=host.querySelector(selector);if(el)el.onclick=fn;};
   bind('[data-import-all]',()=>running?(opened=true,draw()):submit('/api/sources/import-all'));
   bind('[data-import-retry]',()=>submit('/api/sources/imports/'+encodeURIComponent(batch.id)+'/retry'));
   bind('[data-import-refresh]',()=>refresh());
   bind('[data-import-jobs]',()=>navigate('matching'));
   for(const b of host.querySelectorAll('[data-import-source]'))b.onclick=()=>navigate('source_jobs',{sourceID:b.dataset.importSource,page:1});
   const detail=host.querySelector('details');if(detail)detail.ontoggle=()=>{opened=detail.open;};
  }
  function poll(){clear();if(alive()&&batch?.state==='RUNNING')timer=setTimeout(()=>refresh(),5000);}
  async function refresh(){
   if(!alive()||busy)return;clear();const requestRevision=++revision;
   try{const v=await api('/api/sources/imports/latest');if(!alive()||busy||requestRevision!==revision)return;batch=v;warning='';}
   catch(e){if(alive()&&!busy&&requestRevision===revision)warning=e.message||'读取进度失败，请刷新重试。';}
   finally{if(alive()&&!busy&&requestRevision===revision){loading=false;draw();poll();}}
  }
  async function submit(url){
   if(!alive()||busy)return;busy=true;++revision;clear();warning='';draw();
   try{const v=await api(url,'POST',{});if(!alive())return;batch=v;opened=true;}
   catch(e){if(alive())warning=e.code==='SOURCE_IMPORT_COOLDOWN'?'完整导入每 30 分钟可发起一次；失败来源可以单独重试。':e.message||'未能提交导入，请稍后重试。';}
   finally{busy=false;if(alive()){loading=false;draw();poll();}}
  }
  draw();refresh();
  return ()=>{stopped=true;clear();};
 }
 root.CampusSourceBulk={mount};
})(globalThis);
