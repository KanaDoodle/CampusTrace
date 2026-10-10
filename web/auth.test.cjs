const {test}=require('node:test'),assert=require('node:assert/strict');
const A=require('./auth.js');
function setup(api=async()=>({authenticated:true})){
  const values=new Map(),calls=[],elements=new Map(),actions=new Map();let signedIn=0;
  const storage={getItem:key=>values.get(key)||null,setItem:(key,value)=>values.set(key,value),removeItem:key=>values.delete(key)};
  const form={elements:{email:{value:''},password:{value:'',type:'password',focus(){}},remember_email:{checked:false}}};
  elements.set('#login',form);
  for(const id of ['auth-title','auth-submit','register','auth-password-hint','auth-status','auth-password-toggle','auth-login-options'])elements.set('#'+id,{textContent:'',hidden:false,disabled:false,setAttribute(){}});
  const document={querySelector:selector=>elements.get(selector)};
  const auth=A.mount({api:async(...args)=>{calls.push(args);return api(...args);},formAction:(selector,action)=>actions.set(selector,action),UserError:Error,onLogin:()=>signedIn++,storage,document});
  const submit=values=>actions.get('#login')(new Map(Object.entries(values)));
  return {auth,values,calls,elements,form,storage,submit,signedIn:()=>signedIn};
}
test('remembered login sends booleans, stores only opted-in email, and clears the password',async()=>{
  const h=setup();h.form.elements.password.value='go123456';
  await h.submit({email:'me@example.invalid',password:'go123456',remember_email:'on',remember_login:'on'});
  assert.deepEqual(h.calls[0],['/auth/login','POST',{email:'me@example.invalid',password:'go123456',web_session:true,remember:true}]);
  assert.equal(h.signedIn(),1);assert.equal(h.form.elements.password.value,'');
  assert.deepEqual([...h.values.values()],['me@example.invalid']);
  await h.submit({email:'other@example.invalid',password:'old-password-over-twenty'});
  assert.equal(h.values.size,0);assert.equal(h.calls[1][2].remember,false);
});
test('register validates UTF-8 byte boundaries while login accepts existing longer passwords',async()=>{
  const h=setup();h.elements.get('#register').onclick();
  assert.equal(h.form.elements.password.autocomplete,'new-password');
  assert.equal(h.elements.get('#auth-login-options').hidden,true);
  for(const password of ['1234567','a'.repeat(21),'汉'.repeat(7)])await assert.rejects(h.submit({email:'me@example.invalid',password}),/8–20/);
  assert.equal(h.calls.length,0);
  await h.submit({email:'me@example.invalid',password:'汉字好'});
  assert.deepEqual(h.calls[0],['/auth/register','POST',{email:'me@example.invalid',password:'汉字好'}]);
  assert.equal(h.signedIn(),0);assert.equal(h.form.elements.password.autocomplete,'current-password');
  assert.match(h.elements.get('#auth-status').textContent,/账号已创建/);
});
test('failed login does not overwrite remembered email or enable an authenticated page',async()=>{
  const h=setup(async()=>{throw new Error('credentials invalid');});
  A.rememberEmail(h.storage,'saved@example.invalid',true);
  await assert.rejects(h.submit({email:'wrong@example.invalid',password:'PRIVATE-PASSWORD',remember_email:'on'}));
  assert.equal(A.readEmail(h.storage),'saved@example.invalid');assert.equal(h.signedIn(),0);
  assert.equal([...h.values.values()].some(v=>v.includes('PRIVATE')),false);
});
test('automatic restoration tolerates expiry and cannot override a newer login or registration',async()=>{
  let resolve;const h=setup(()=>new Promise(done=>resolve=done));const restoration=h.auth.restore();
  h.elements.get('#register').onclick();resolve({authenticated:true});await restoration;assert.equal(h.signedIn(),0);
  const expired=setup(async()=>{throw Object.assign(new Error('expired'),{status:401});});await expired.auth.restore();
  assert.equal(expired.signedIn(),0);assert.equal(expired.elements.get('#auth-status').textContent,'');
});
test('blocked storage does not block login; reset clears password and returns to login mode',async()=>{
  const h=setup();h.storage.setItem=()=>{throw new Error('blocked');};
  await h.submit({email:'me@example.invalid',password:'go123456',remember_email:'on'});assert.equal(h.signedIn(),1);
  h.elements.get('#register').onclick();h.form.elements.password.value='PRIVATE';h.auth.reset();
  assert.equal(h.form.elements.password.value,'');assert.equal(h.form.elements.password.autocomplete,'current-password');
  assert.equal(h.elements.get('#auth-login-options').hidden,false);
});
