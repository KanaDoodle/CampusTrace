(function(root){
'use strict';
const kind='evidence-explanation';
const terminal=v=>!v||v.state!=='RUNNING'||v.can_resume===true;
const label=v=>({RUNNING:'正在生成说明',PENDING:'等待确认后继续',PAUSED:'说明已中断',COMPLETED:'说明已完成',CANCELLED:'已停止生成',STALE:'依据已变化',FAILED:'本次尝试已结束'}[v?.state]||'说明任务');
function canContinue(v){return !!v?.can_resume&&['PENDING','PAUSED','RUNNING'].includes(v.state)&&Number(v.resumes)<5;}
function needsRetryReview(v){return ['DISPATCHED','UNKNOWN'].includes(v?.narration?.provider_state);}
function accepts(previous,next){
 if(!previous)return true;if(previous.id!==next.id)return false;
 if(Number(next.resumes)!==Number(previous.resumes))return Number(next.resumes)>Number(previous.resumes);
 if(Number(next.narration?.revision||0)<Number(previous.narration?.revision||0))return false;
 if(['COMPLETED','CANCELLED','STALE','FAILED'].includes(previous.state)&&next.state==='RUNNING')return false;
 return true;
}
function render(v,esc,{compact=false}={}){
 const source=v.narration?.evidence_kind==='fixture-no-model'?'本地无模型测试内容':'模型生成说明，尚未核实';
 return `<article class="agent-execution" data-narration-record="${esc(v.id)}"><h3>依据说明</h3><p class="meta">${esc(label(v))} · ${esc(source)}</p>${!compact?`<p class="preserve-lines">${esc(['CANCELLED','STALE'].includes(v.state)?'':v.answer||v.narration?.draft||'尚未形成完整说明。')}</p>`:''}<p class="meta">依据仍以原任务记录为准；这段文字不会修改求职资料或执行操作。${Number(v.resumes)>1?'此前模型调用可能已发生，当前为第 '+Number(v.resumes)+' 次尝试。':''}</p><div class="actions"><button type="button" class="btn btn-small" data-narration-open="${esc(v.id)}">查看说明与进度</button>${canContinue(v)?`<button type="button" class="btn btn-small" data-narration-resume="${esc(v.id)}">重新核对后继续</button>`:''}${['RUNNING','PENDING','PAUSED'].includes(v.state)?`<button type="button" class="text-btn" data-narration-cancel="${esc(v.id)}">停止生成</button>`:''}</div></article>`;
}
function parser(onEvent){
 let buffer='';return {push(value){buffer=(buffer+value).replace(/\r\n/g,'\n');if(buffer.length>262144)throw Error('说明快照过大');for(;;){const end=buffer.indexOf('\n\n');if(end<0)return;const frame=buffer.slice(0,end).replace(/\r/g,'');buffer=buffer.slice(end+2);const lines=frame.split('\n'),type=lines.find(v=>v.startsWith('event:'))?.slice(6).trim(),id=lines.find(v=>v.startsWith('id:'))?.slice(3).trim();if(type!=='snapshot')continue;const data=lines.filter(v=>v.startsWith('data:')).map(v=>v.slice(5).trimStart()).join('\n');onEvent({id,data:JSON.parse(data)});}},finish(){if(buffer.trim()&&!buffer.startsWith(':'))throw Error('说明快照未完整接收');}};
}
function subscribe({id,fetchStream,active=()=>true,onSnapshot,onError=()=>{},delay=250}){
 let stopped=false,last='',previous=null,attempts=0;const abort=new AbortController();
 const dispose=()=>{stopped=true;abort.abort();};
 const promise=(async()=>{while(!stopped&&active()){
  try{
   const response=await fetchStream('/api/agent/executions/'+encodeURIComponent(id)+'/stream',{signal:abort.signal,lastEventID:last});
   if(!active()||stopped){await response.body?.cancel?.();return;}
   const reader=response.body.getReader(),decoder=new TextDecoder();
   const frames=parser(event=>{if(stopped||!active()||event.data?.id!==id||event.id===last||!accepts(previous,event.data))return;previous=event.data;last=event.id||'';onSnapshot(event.data);if(terminal(event.data))stopped=true;});
   try{for(;;){const result=await reader.read();if(result.done){frames.push(decoder.decode());frames.finish();break;}frames.push(decoder.decode(result.value,{stream:true}));if(stopped||!active())break;}}
   finally{await reader.cancel().catch(()=>{});reader.releaseLock?.();}
   if(stopped||!active())return;
  }catch(error){if(stopped||!active()||abort.signal.aborted)return;if(++attempts>=3){onError(error);return;}}
  if(++attempts>=4){onError(Error('说明连接已断开，任务仍可在记录中查看。'));return;}
  await new Promise(resolve=>{const timer=root.setTimeout(resolve,delay);abort.signal.addEventListener('abort',()=>{root.clearTimeout(timer);resolve();},{once:true});});
 }} )();return {dispose,promise};
}
function bind(h){
 const {api,esc,UserError}=h,active=h.active||(()=>true);let enabled=false,subscription=null,current=null,revision=0,disposed=false,opening=false;
 const host=root.document.createElement('section');host.id='agent-narration-panel';host.className='agent-panel';host.hidden=true;
 host.innerHTML='<h2>依据说明</h2><p data-narration-status role="status"></p><p data-narration-kind class="meta"></p><p class="meta">生成文本仅供参考，以原任务依据为准。</p><div data-narration-text class="preserve-lines" data-scroll-region></div><div class="actions"><button type="button" data-narration-stop>停止生成</button><button type="button" data-narration-reconnect>重新读取进度</button><button type="button" data-narration-continue>重新核对后继续</button></div>';
 const parent=root.document.querySelector('#agent-execution-current');parent?.after(host);
 const dialog=root.document.createElement('dialog');dialog.id='agent-narration-review';dialog.innerHTML='<form method="dialog"><h2>核对要发送的资料</h2><p data-narration-review-status role="status"></p><p>这会调用已配置的模型生成说明。请核对以下脱敏内容，生成结果不会自动写入业务记录。</p><pre class="narration-preview" data-narration-preview></pre><label data-narration-unknown-label hidden><input type="checkbox" data-narration-unknown> 上次调用结果未知，再次生成可能重复调用或计费，我已核对并愿意继续。</label><div class="actions"><button type="button" data-narration-confirm>确认发送并生成说明</button><button type="button" data-narration-close>暂不生成</button></div></form>';
 root.document.body.append(dialog);
 const $=selector=>host.querySelector(selector),d$=selector=>dialog.querySelector(selector);
 const requestMask=()=>({mask_name:h.identity?.().mask_name||''});
 function fail(error){if(active()&&!disposed)h.fail(error instanceof UserError?error:new UserError('暂时无法读取说明，请稍后重新查看。'));}
 function apply(v){if(!active()||disposed||!accepts(current,v))return;const ended=current&&!terminal(current)&&terminal(v);current=v;host.hidden=false;$('[data-narration-status]').textContent=label(v);$('[data-narration-kind]').textContent=v.narration?.evidence_kind==='fixture-no-model'?'本地无模型测试内容，不代表真实推理效果。':'模型说明，尚未核实。';$('[data-narration-text]').textContent=v.state==='STALE'||v.state==='CANCELLED'?'':v.answer||v.narration?.draft||'';$('[data-narration-stop]').hidden=!['RUNNING','PAUSED','PENDING'].includes(v.state);$('[data-narration-continue]').hidden=!canContinue(v);if(ended)Promise.resolve(h.refresh?.()).catch(()=>{});}
 async function open(id){const turn=++revision;opening=true;subscription?.dispose();current=null;const v=await api('/api/agent/executions/'+encodeURIComponent(id));if(!active()||disposed||turn!==revision)return;opening=false;apply(v);if(!terminal(v)&&h.fetchStream){subscription=subscribe({id,fetchStream:h.fetchStream,active:()=>active()&&!disposed&&turn===revision,onSnapshot:apply,onError:error=>{if(active())$('[data-narration-status]').textContent='连接已断开，后台任务不会因此重新调用。可重新读取进度。';}});}}
 async function cancel(id){await api('/api/agent/executions/'+encodeURIComponent(id)+'/cancel','POST',{});if(active())await open(id);}
 async function review(id,resuming=false){
  if(!enabled)throw new UserError('说明服务尚未配置，请先使用原任务中的依据。');
  const turn=++revision;let task=null,parentID=id;if(resuming){task=await api('/api/agent/executions/'+encodeURIComponent(id));parentID=task.narration?.parent_id;if(!parentID)throw new UserError('原任务依据已不可用，请重新读取。');}
  const mask=requestMask(),value=await api('/api/agent/executions/'+encodeURIComponent(parentID)+'/narration/preview','POST',mask);if(!active()||disposed||turn!==revision)return;
  const nonce=root.crypto.randomUUID().replace(/-/g,''),unknown=resuming&&needsRetryReview(task);d$('[data-narration-preview]').textContent=(value.messages||[]).map(v=>v.role+'\n'+v.content).join('\n\n');d$('[data-narration-review-status]').textContent=(value.evidence_kind==='fixture-no-model'?'本地无模型测试 · ':'将发送给 ')+(value.model||'已配置模型');d$('[data-narration-unknown-label]').hidden=!unknown;const checkbox=d$('[data-narration-unknown]');checkbox.checked=false;const confirm=d$('[data-narration-confirm]');confirm.disabled=unknown;checkbox.onchange=()=>{confirm.disabled=unknown&&!checkbox.checked;};
  confirm.onclick=async()=>{if(!active()||disposed||turn!==revision||unknown&&!checkbox.checked)return;confirm.disabled=true;try{const out=await api('/api/agent/executions/'+encodeURIComponent(resuming?id:parentID)+'/narration'+(resuming?'/resume':''),'POST',{...mask,request_key:nonce,review_hash:value.review_hash,confirm:true,retry_unknown:unknown&&checkbox.checked});if(!active()||disposed)return;dialog.close();await open(out.id);await h.refresh?.();}catch(error){if(active()&&!disposed){d$('[data-narration-review-status]').textContent=error.message||'尚未成功提交，请重新核对。';confirm.disabled=unknown&&!checkbox.checked;}}};
  d$('[data-narration-close]').onclick=()=>dialog.close();dialog.showModal();
 }
 function wire(){if(!active()||disposed)return;if(!current&&!opening){const saved=root.document.querySelector('#agent-execution-current [data-narration-open]');if(saved)open(saved.dataset.narrationOpen).catch(error=>{opening=false;fail(error);});}for(const button of root.document.querySelectorAll('[data-execution-narrate]')){button.disabled=!enabled;button.onclick=()=>review(button.dataset.executionNarrate).catch(fail);}for(const button of root.document.querySelectorAll('[data-narration-open]'))button.onclick=()=>open(button.dataset.narrationOpen).catch(fail);for(const button of root.document.querySelectorAll('[data-narration-resume]'))button.onclick=()=>review(button.dataset.narrationResume,true).catch(fail);for(const button of root.document.querySelectorAll('[data-narration-cancel]'))button.onclick=()=>cancel(button.dataset.narrationCancel).catch(fail);}
 $('[data-narration-stop]').onclick=()=>{if(current)cancel(current.id).catch(fail);};$('[data-narration-reconnect]').onclick=()=>{if(current)open(current.id).catch(fail);};$('[data-narration-continue]').onclick=()=>{if(current)review(current.id,true).catch(fail);};
 api('/api/agent/narration/capabilities').then(v=>{if(active()&&!disposed){enabled=!!v.enabled;wire();}}).catch(()=>{});
 const timer=root.setInterval(()=>{if(!active())dispose();},250);
 function dispose(){if(disposed)return;disposed=true;revision++;subscription?.dispose();root.clearInterval(timer);dialog.close();dialog.remove();}
 return {wire,open,review,dispose};
}
root.CampusNarration={kind,terminal,label,canContinue,needsRetryReview,accepts,render,parser,subscribe,bind};if(typeof module!=='undefined')module.exports=root.CampusNarration;
})(typeof window!=='undefined'?window:globalThis);
