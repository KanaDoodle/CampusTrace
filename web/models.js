'use strict';
const CampusModels=(function(root){
  const presets=Object.freeze([
    Object.freeze({id:'gpt-6-luna',provider:'openai',name:'GPT-6 Luna',url:'https://api.openai.com/v1/chat/completions',model:'gpt-6-luna'}),
    Object.freeze({id:'gpt-6-sol',provider:'openai',name:'GPT-6 Sol',url:'https://api.openai.com/v1/chat/completions',model:'gpt-6-sol'}),
    Object.freeze({id:'deepseek-flash',provider:'deepseek',name:'DeepSeek Flash',url:'https://api.deepseek.com/chat/completions',model:'deepseek-flash'}),
    Object.freeze({id:'deepseek-v4-pro',provider:'deepseek',name:'DeepSeek V4 Pro',url:'https://api.deepseek.com/chat/completions',model:'deepseek-v4-pro'})
  ]);
  const providers=Object.freeze({openai:'OpenAI',deepseek:'DeepSeek'});
  let keys={},active='default',boundUser='';
  const storageKey=id=>'campustrace:model-keys:v1:'+id;
  function storage(){
    if(!root.localStorage)throw new Error('此浏览器无法使用本地存储，请启用后再保存密钥。');
    return root.localStorage;
  }
  function bindUser(id){
    const next=String(id||'');
    if(!/^[a-zA-Z0-9_-]{1,128}$/.test(next))throw new Error('无法确认当前账号，暂不能读取本机模型密钥。');
    if(next===boundUser)return;
    keys={};active='default';boundUser='';
    let saved;
    try{saved=storage().getItem(storageKey(next));}catch{boundUser=next;return;}
    if(saved){
      try{
        const data=JSON.parse(saved);
        for(const provider of Object.keys(providers))if(typeof data.keys?.[provider]==='string')keys[provider]=data.keys[provider];
        if(presets.some(item=>item.id===data.active&&keys[item.provider]))active=data.active;
      }catch{keys={};}
    }
    boundUser=next;
  }
  function persist(nextKeys,nextActive){
    if(!boundUser)throw new Error('请先登录，再保存模型密钥。');
    try{storage().setItem(storageKey(boundUser),JSON.stringify({keys:nextKeys,active:nextActive}));}
    catch{throw new Error('无法在此浏览器保存密钥，请检查本地存储设置或可用空间。');}
    keys=nextKeys;active=nextActive;
  }
  function saveKey(provider,value){
    if(!Object.hasOwn(providers,provider))throw new Error('未知的模型提供商。');
    const key=String(value||'').trim();
    if(!key||key.length>1024||/[\r\n]/.test(key))throw new Error('请填写有效的 API 密钥（不超过 1024 字符）。');
    if(root.location&&root.location.protocol!=='https:'&&!['localhost','127.0.0.1','[::1]'].includes(root.location.hostname))throw new Error('请使用本机地址或 HTTPS 页面保存 API 密钥。');
    const nextKeys={...keys,[provider]:key};
    const nextActive=active==='default'?presets.find(item=>item.provider===provider).id:active;
    persist(nextKeys,nextActive);
  }
  function removeKey(provider){
    if(!Object.hasOwn(providers,provider))throw new Error('未知的模型提供商。');
    const nextKeys={...keys};delete nextKeys[provider];
    const nextActive=presets.some(item=>item.id===active&&item.provider===provider)?'default':active;
    persist(nextKeys,nextActive);
  }
  function select(id){
    const item=presets.find(entry=>entry.id===id);
    if(id!=='default'&&!item)throw new Error('模型预设不存在。');
    if(item&&!keys[item.provider])throw new Error('请先填写该提供商的 API 密钥。');
    persist(keys,id);
  }
  function lock(){keys={};active='default';boundUser='';}
  function requestConfig(){
    const item=presets.find(entry=>entry.id===active);
    return item&&keys[item.provider]?{url:item.url,model:item.model,api_key:keys[item.provider]}:undefined;
  }
  function available(capabilities){return !!requestConfig()||!!capabilities?.model_available;}
  function label(capabilities){
    const item=presets.find(entry=>entry.id===active);
    return item&&keys[item.provider]?item.name:capabilities?.model_available?'服务器默认 · '+(capabilities.model||'已配置模型'):'尚未配置外部模型';
  }
  function sessionID(capabilities){return active==='default'?(capabilities?.model_available?'web-default':'web-demo'):'web-'+active;}
  async function page(set,heading,{api,esc,formAction,UserError}){
    const capabilities=await api('/api/profile/resume/capabilities');
    bindUser(capabilities.user_id);
    const notice=message=>{document.querySelector('#notice').textContent=message;};
    const render=()=>{
      const cards=presets.map(item=>`<article class="card"><h4>${esc(item.name)}</h4><p class="meta">${esc(providers[item.provider])} · ${esc(item.model)}</p><button type="button" data-select-model="${item.id}" ${!keys[item.provider]||active===item.id?'disabled':''}>${active===item.id?'正在使用':keys[item.provider]?'选用':'先填写密钥'}</button></article>`).join('');
      const forms=Object.entries(providers).map(([id,name])=>`<form data-provider="${id}"><h4>${esc(name)} API 密钥</h4><p class="meta">${keys[id]?'已保存在这个浏览器中，可直接选择上方模型。':'尚未填写。'}</p><label>API 密钥<input name="api_key" type="password" required maxlength="1024" autocomplete="off" placeholder="粘贴 ${esc(name)} API 密钥"></label><div class="actions"><button>保存到本机${keys[id]?'并替换旧密钥':''}</button>${keys[id]?`<button type="button" data-remove-key="${id}">删除本机密钥</button>`:''}</div></form>`).join('');
      if(!set(`${heading}<p>填写提供商的 API 密钥，然后选择内置模型。接口地址和模型标识由 CampusTrace 维护，无需手动配置。同一提供商的密钥可用于它的多个预设。</p><p class="pending-note">当前选择：${esc(label(capabilities))}。密钥保存在此浏览器、当前账号的本地存储中；刷新和重新登录后仍可用。本地存储未加密，同一设备上能访问此浏览器资料的人可能读到密钥，请只在自己的设备上保存。调用模型时，浏览器会把密钥发给 CampusTrace 服务，再由服务发给提供商；服务端不保存密钥。所选提供商会收到你确认发送的脱敏简历文字，或求职问答问题与相关资料。</p>${capabilities.model_available?`<button id="select-default-model" type="button" ${active==='default'?'disabled':''}>${active==='default'?'正在使用服务器默认模型':'选用服务器默认模型'}</button>`:''}<h3>内置模型</h3><div class="model-presets">${cards}</div><h3>提供商密钥</h3>${forms}`))return;
      document.querySelector('#select-default-model')?.addEventListener('click',()=>{try{select('default');render();notice('已选用服务器默认模型。');}catch(error){notice(error.message);}});
      for(const button of document.querySelectorAll('[data-select-model]'))button.onclick=()=>{try{select(button.dataset.selectModel);render();notice('已切换模型。');}catch(error){notice(error.message);}};
      for(const form of document.querySelectorAll('[data-provider]'))formAction(`[data-provider="${form.dataset.provider}"]`,async data=>{try{saveKey(form.dataset.provider,data.get('api_key'));render();notice('API 密钥已保存在此浏览器。');}catch(error){throw new UserError(error.message);}});
      for(const button of document.querySelectorAll('[data-remove-key]'))button.onclick=()=>{try{removeKey(button.dataset.removeKey);render();notice('该提供商的密钥已从此浏览器删除。');}catch(error){notice(error.message);}};
    };
    render();
  }
  const api={presets,bindUser,saveKey,removeKey,select,lock,requestConfig,available,label,sessionID,page};
  if(typeof module==='object'&&module.exports)module.exports=api;
  root.CampusModels=api;
  return api;
})(typeof window==='undefined'?globalThis:window);
