'use strict';
const CampusModels=(function(root){
  let entries=[];
  let active='default';
  function add(value){
    const name=String(value.name||'').trim(),url=String(value.url||'').trim(),model=String(value.model||'').trim(),key=String(value.api_key||'').trim();
    let parsed;
    try{parsed=new URL(url);}catch{throw new Error('请填写完整的 HTTPS Chat Completions 接口地址。');}
    const host=parsed.hostname.toLowerCase().replace(/\.$/,'');
    if(!name||name.length>60||!model||model.length>128||!key||key.length>1024||url.length>2048||parsed.protocol!=='https:'||parsed.username||parsed.password||parsed.search||parsed.hash||!parsed.pathname||!host.includes('.')||/^(\d+\.){3}\d+$/.test(host)||host.startsWith('[')||['.local','.localhost','.internal'].some(suffix=>host.endsWith(suffix))||(parsed.port&&parsed.port!=='443'))throw new Error('请填写名称、公开 HTTPS 接口地址、模型标识和密钥；地址不要带查询参数。');
    const item={id:root.crypto.randomUUID(),name,url,model,api_key:key};
    entries.push(item);active=item.id;return item.id;
  }
  function select(id){if(id!=='default'&&!entries.some(item=>item.id===id))throw new Error('模型配置不存在。');active=id;}
  function remove(id){entries=entries.filter(item=>item.id!==id);if(active===id)active='default';}
  function clear(){entries=[];active='default';}
  function requestConfig(){const item=entries.find(entry=>entry.id===active);return item?{url:item.url,model:item.model,api_key:item.api_key}:undefined;}
  function available(capabilities){return !!requestConfig()||!!capabilities?.model_available;}
  function label(capabilities){const item=entries.find(entry=>entry.id===active);return item?`${item.name} · ${item.model}`:capabilities?.model_available?`服务器默认 · ${capabilities.model||'已配置模型'}`:'尚未配置外部模型';}
  function sessionID(capabilities){return active==='default'?(capabilities?.model_available?'web-default':'web-demo'):'web-'+active;}
  async function page(set,heading,{api,esc,formAction,UserError}){
    const capabilities=await api('/api/profile/resume/capabilities');
    const notice=message=>{document.querySelector('#notice').textContent=message;};
    const render=()=>{
      const rows=entries.map(item=>`<article class="card"><h4>${esc(item.name)}</h4><p>${esc(item.model)}</p><p class="meta">${esc(item.url)} · 密钥已在本次页面中提供</p><div class="actions"><button type="button" data-select-model="${esc(item.id)}" ${active===item.id?'disabled':''}>${active===item.id?'正在使用':'选用'}</button><button type="button" data-remove-model="${esc(item.id)}">移除</button></div></article>`).join('');
      if(!set(`${heading}<p>在这里选择简历草稿和求职问答使用的外部模型。密钥只保留在当前页面的内存中；刷新、关闭页面或退出登录后需要重新填写。每次调用时，密钥由浏览器发给 CampusTrace 服务，再由服务转发给所选提供商；CampusTrace 不会将密钥存入数据库。</p><p class="pending-note">当前选择：${esc(label(capabilities))}。所选提供商会收到本次发送的脱敏简历文字，或求职问答问题与相关资料。只支持公开 HTTPS 的 OpenAI-compatible Chat Completions 接口，不支持内网地址。</p>${capabilities.model_available?`<button id="select-default-model" type="button" ${active==='default'?'disabled':''}>${active==='default'?'正在使用服务器默认模型':'选用服务器默认模型'}</button>`:''}<h3>本次页面中的模型</h3>${rows||'<p class="empty">还没有添加模型。</p>'}<h3>添加模型</h3><form id="add-model"><div class="form-grid"><label>显示名称<input name="name" maxlength="60" required placeholder="例如：我的模型"></label><label>模型标识<input name="model" maxlength="128" required placeholder="服务商提供的模型 ID"></label></div><label>Chat Completions 完整接口地址<input name="url" type="url" required maxlength="2048" placeholder="https://服务商地址/v1/chat/completions"></label><label>API 密钥<input name="api_key" type="password" required maxlength="1024" autocomplete="off"></label><button>添加并选用</button></form><p class="meta">密钥不保存；页面刷新后请重新填写。不要在这里粘贴简历或其他个人信息。</p>`))return;
      formAction('#add-model',async data=>{try{add(Object.fromEntries(data));render();notice('模型已在当前页面添加并选用。');}catch(error){throw new UserError(error.message);}});
      document.querySelector('#select-default-model')?.addEventListener('click',()=>{select('default');render();notice('已选用服务器默认模型。');});
      for(const button of document.querySelectorAll('[data-select-model]'))button.onclick=()=>{select(button.dataset.selectModel);render();notice('已切换模型。');};
      for(const button of document.querySelectorAll('[data-remove-model]'))button.onclick=()=>{remove(button.dataset.removeModel);render();notice('该模型已从当前页面移除。');};
    };
    render();
  }
  const api={add,select,remove,clear,requestConfig,available,label,sessionID,page};
  if(typeof module==='object'&&module.exports)module.exports=api;
  root.CampusModels=api;
  return api;
})(typeof window==='undefined'?globalThis:window);
