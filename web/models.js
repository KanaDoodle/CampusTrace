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
    let provider=presets.find(item=>item.id===active)?.provider||'deepseek';
    const U=root.CampusUI;
    const render=()=>{
      const order=['deepseek','openai'];
      const panels=order.map(id=>`<div class="provider-panel" id="provider-${id}" role="tabpanel" aria-labelledby="provider-tab-${id}" ${provider===id?'':'hidden'}><div class="settings-body"><h3>${esc(providers[id])}</h3><p class="form-note">接口地址和模型标识由 CampusTrace 维护，同一密钥可用于多个预设。</p><div class="model-presets">${presets.filter(item=>item.provider===id).map(item=>`<article class="card ${active===item.id?'is-current':''}"><h4>${esc(item.name)}</h4><p class="meta">${active===item.id?'当前使用':keys[id]?'已配置提供商密钥':'先保存提供商密钥'}</p><button class="btn btn-small" type="button" data-select-model="${item.id}" ${!keys[id]||active===item.id?'disabled':''}>${active===item.id?'正在使用':keys[id]?'选用':'先填写密钥'}</button></article>`).join('')}</div><form data-provider="${id}"><label>${esc(providers[id])} API 密钥<input name="api_key" type="password" required maxlength="1024" autocomplete="off" placeholder="${keys[id]?'已保存；填写新密钥可替换':'粘贴 '+esc(providers[id])+' API 密钥'}"></label><p class="form-note">${keys[id]?'已保存在这个浏览器、当前账号的本地存储中。':'尚未配置，保存后可选择上方模型。'}</p><div class="actions"><button class="btn btn-primary">保存到本机${keys[id]?'并替换旧密钥':''}</button>${keys[id]?`<button class="btn" type="button" data-remove-key="${id}">删除本机密钥</button>`:''}</div></form></div></div>`).join('');
      const html=U.heading('模型设置','选择服务商，分析时直接使用已配置的模型。')+`<div class="model-current">当前选择：<strong>${esc(label(capabilities))}</strong>${capabilities.model_available?` <button id="select-default-model" class="btn btn-small" type="button" ${active==='default'?'disabled':''}>${active==='default'?'正在使用服务器默认模型':'选用服务器默认模型'}</button>`:''}</div><div class="settings-layout"><section class="settings-panel"><div class="profile-tabs" role="tablist" aria-label="模型服务商">${order.map(id=>`<button id="provider-tab-${id}" type="button" role="tab" data-view-tab data-provider-tab="${id}" aria-selected="${provider===id}" aria-controls="provider-${id}" tabindex="${provider===id?0:-1}">${esc(providers[id])}</button>`).join('')}</div>${panels}</section><aside class="side-info"><h3>你掌握发送时机</h3><p>配置或切换模型不会自动发送资料。发起分析前，仍需核对脱敏文字并确认。</p><div class="side-rule"></div><h3>本地密钥如何使用</h3><p>密钥按账号保存在此浏览器，服务端不保存。调用时经 CampusTrace 服务转发给所选提供商。</p><details><summary>本机存储与外发说明</summary><p>本地存储未加密，请只在自己的设备保存密钥。能访问此浏览器资料的人可能读到密钥。所选提供商会收到你确认发送的脱敏简历文字，或求职问答问题与相关资料。</p></details><div class="note-box">API 调用独立计费。也可从岗位库导出分析包，手动上传 ChatGPT，无需填写 API 密钥。</div></aside></div>`;
      if(!set(html))return;
      for(const button of document.querySelectorAll('[data-provider-tab]'))button.onclick=()=>{provider=button.dataset.providerTab;};
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
