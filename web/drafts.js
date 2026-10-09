'use strict';
(function(root){
 const TTL=7*86400000,LIMIT=250000;
 let bound='';
 const key=(user,scope)=>'campustrace:local-drafts:v1:'+user+':'+scope;
 const preference=user=>'campustrace:local-drafts-enabled:v1:'+user;
 function enabled(user){try{return root.localStorage?.getItem(preference(user))==='yes';}catch{return false;}}
 function validField(v){return v&&typeof v.name==='string'&&v.name.length<150&&typeof v.type==='string'&&typeof v.value==='string'&&v.value.length<=60000&&typeof v.checked==='boolean'&&!/password|api.?key|secret|token|resume|consent/i.test(v.name)&&v.type!=='file'&&v.type!=='password';}
 function load(user,scope){
  try{const raw=root.localStorage?.getItem(key(user,scope));if(!raw||raw.length>LIMIT)return [];const v=JSON.parse(raw);
   if(!Number.isFinite(v.updated)||v.updated>Date.now()+60000||Date.now()-v.updated>TTL){root.localStorage.removeItem(key(user,scope));return [];}
   if(!Array.isArray(v.records)||v.records.length>50)return [];
   return v.records.filter(r=>r&&typeof r.id==='string'&&r.id.length<=200&&Array.isArray(r.baseline)&&Array.isArray(r.value)&&r.baseline.length<=100&&r.value.length<=100&&r.baseline.every(validField)&&r.value.every(validField));
  }catch{return [];}
 }
 function write(user,scope,records){
  if(!enabled(user)||!Array.isArray(records)||records.length>50||records.some(r=>!r||typeof r.id!=='string'||r.id.length>200||!Array.isArray(r.baseline)||!Array.isArray(r.value)||r.baseline.length>100||r.value.length>100||!r.baseline.every(validField)||!r.value.every(validField)))return false;
  try{const raw=JSON.stringify({updated:Date.now(),records});if(raw.length>LIMIT)return false;if(records.length)root.localStorage.setItem(key(user,scope),raw);else root.localStorage.removeItem(key(user,scope));return true;}catch{return false;}
 }
 function clear(user,scope){try{root.localStorage?.removeItem(key(user,scope));}catch{}}
 function mount({user,scope,tracker,allow=()=>true,prepare,active=()=>true,afterRestore=()=>{},help='仅保留本页编辑内容，不包含原始简历、模型密钥或外发确认。',host=()=>root.document.querySelector('#local-drafts')}){
  bound=user;let pending=enabled(user)?load(user,scope).filter(r=>allow(r.id)):[],message='',timer,stopped=false;
  const alive=()=>!stopped&&active()&&bound===user;
  function persist(){if(!alive()||!enabled(user)||pending.length)return;const rows=tracker.entries(allow).filter(r=>r.baseline.every(validField)&&r.value.every(validField));message=write(user,scope,rows)?(rows.length?'草稿已留在本机，尚未保存到求职记录。':'没有未保存的草稿。'):'本机草稿暂时无法保存，请先保存正式记录。';draw();}
  function draw(){const box=host();if(!box||!alive())return;box.innerHTML='<label><input type="checkbox" data-drafts-enable '+(enabled(user)?'checked':'')+'>在本机保留编辑草稿（7 天）</label><p data-drafts-message></p>'+(pending.length?'<div class="actions"><button type="button" class="btn btn-small" data-drafts-restore>恢复上次草稿</button><button type="button" class="text-btn" data-drafts-download>下载旧草稿</button><button type="button" class="text-btn" data-drafts-discard>丢弃上次草稿</button></div>':'');box.querySelector('[data-drafts-message]').textContent=pending.length?(message||`发现 ${pending.length} 份未保存草稿。恢复前会核对当前记录；草稿不会自动进入匹配。`):message||(enabled(user)?'已开启。':'默认关闭。')+help;
   box.querySelector('[data-drafts-enable]').onchange=e=>{try{root.localStorage?.setItem(preference(user),e.target.checked?'yes':'no');}catch{message='浏览器不允许保存本机草稿。';draw();return;}if(!e.target.checked){clear(user,scope);pending=[];message='已关闭并清除本页本机草稿。';draw();}else persist();};
   const restore=box.querySelector('[data-drafts-restore]');if(restore)restore.onclick=()=>{const result=tracker.hydrate(pending,prepare);pending=pending.filter(r=>result.conflictIDs.includes(r.id));message=result.conflicts?`已恢复 ${result.restored} 份；${result.conflicts} 份因记录或当前编辑已变化，未覆盖。可下载旧草稿后自行核对。`:`已恢复 ${result.restored} 份草稿，请核对后保存。`;afterRestore();write(user,scope,[...pending,...tracker.entries(allow).filter(r=>!pending.some(p=>p.id===r.id))]);draw();};
   const download=box.querySelector('[data-drafts-download]');if(download)download.onclick=()=>{const url=root.URL.createObjectURL(new Blob([JSON.stringify({scope,records:pending},null,2)],{type:'application/json'}));const a=root.document.createElement('a');a.href=url;a.download='CampusTrace-本机编辑草稿.json';a.click();setTimeout(()=>root.URL.revokeObjectURL(url),1000);};
   const discard=box.querySelector('[data-drafts-discard]');if(discard)discard.onclick=()=>{pending=[];clear(user,scope);message='已丢弃上次草稿，当前编辑保留。';persist();};
  }
  const changed=e=>{if(!alive()||!e.target?.closest?.('form')||!enabled(user))return;clearTimeout(timer);timer=setTimeout(persist,350);};
  root.document.addEventListener?.('input',changed);root.document.addEventListener?.('change',changed);
  const flush=()=>{clearTimeout(timer);persist();};root.addEventListener?.('beforeunload',flush);
  draw();return {draw,flush,dispose(){flush();stopped=true;root.document.removeEventListener?.('input',changed);root.document.removeEventListener?.('change',changed);root.removeEventListener?.('beforeunload',flush);}};
 }
 const api={enabled,load,write,clear,mount,lock(){bound='';}};root.CampusDrafts=api;if(typeof module==='object'&&module.exports)module.exports=api;
})(typeof window==='undefined'?globalThis:window);
