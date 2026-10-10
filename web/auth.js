(function(root){
  'use strict';
  const emailKey='campustrace:remembered-email:v1';
  function readEmail(storage){try{const value=storage.getItem(emailKey)||'';return value.length<=254&&!/[\r\n\0]/.test(value)?value:'';}catch{return '';}}
  function rememberEmail(storage,email,remember){try{if(remember)storage.setItem(emailKey,email);else storage.removeItem(emailKey);return true;}catch{return false;}}
  function passwordBytes(value){return new TextEncoder().encode(value).length;}
  function mount({api,formAction,UserError,onLogin,storage=root.localStorage,document=root.document}){
    const form=document.querySelector('#login'),title=document.querySelector('#auth-title'),submit=document.querySelector('#auth-submit'),switcher=document.querySelector('#register'),hint=document.querySelector('#auth-password-hint'),status=document.querySelector('#auth-status'),toggle=document.querySelector('#auth-password-toggle');
    const email=form.elements.email,password=form.elements.password,remember=form.elements.remember_email;
    const saved=readEmail(storage);email.value=saved;remember.checked=!!saved;
    let registering=false,revision=0,busy=false;
    const setMode=value=>{
      registering=value;title.textContent=value?'注册 CampusTrace':'登录 CampusTrace';submit.textContent=value?'创建账号':'登录';switcher.textContent=value?'返回登录':'注册账号';
      password.autocomplete=value?'new-password':'current-password';form.action=value?'/auth/register':'/auth/login';hint.hidden=!value;document.querySelector('#auth-login-options').hidden=value;status.textContent='';
    };
    switcher.onclick=()=>{if(!busy){revision++;setMode(!registering);}};
    toggle.onclick=()=>{const visible=password.type==='password';password.type=visible?'text':'password';toggle.textContent=visible?'隐藏密码':'显示密码';toggle.setAttribute('aria-pressed',String(visible));};
    formAction('#login',async data=>{
      revision++;busy=true;switcher.disabled=true;status.textContent='';
      const credentials={email:String(data.get('email')||'').trim(),password:String(data.get('password')||'')};
      try{
        if(registering){
          const size=passwordBytes(credentials.password);
          if(size<8||size>20)throw new UserError('密码需为 8–20 字节；字母和数字各占 1 字节，汉字通常占 3 字节。');
          await api('/auth/register','POST',credentials);setMode(false);status.textContent='账号已创建，请登录。';password.focus();
        }else{
          const result=await api('/auth/login','POST',{...credentials,web_session:true,remember:data.get('remember_login')==='on'});
          if(result.authenticated!==true)throw new UserError('登录结果暂时无法确认，请重试。');
          rememberEmail(storage,credentials.email,data.get('remember_email')==='on');
          password.value='';password.type='password';toggle.textContent='显示密码';toggle.setAttribute('aria-pressed','false');onLogin();
        }
      }finally{busy=false;switcher.disabled=false;}
    });
    setMode(false);
    return {
      async restore(){
        const current=revision;
        try{const result=await api('/auth/session');if(current===revision&&result.authenticated===true){password.value='';onLogin();}}
        catch(error){if(current===revision&&error.status!==401)status.textContent='无法检查上次登录，请手动登录。';}
      },
      reset(){revision++;setMode(false);password.value='';password.type='password';toggle.textContent='显示密码';toggle.setAttribute('aria-pressed','false');},
    };
  }
  const api={readEmail,rememberEmail,passwordBytes,mount};root.CampusAuth=api;
  if(typeof module==='object'&&module.exports)module.exports=api;
})(typeof window==='undefined'?globalThis:window);
