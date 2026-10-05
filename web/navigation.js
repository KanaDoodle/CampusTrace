'use strict';
const CampusNavigation=(function(root){
  let current=null,leaving=false;
  function register(options){current=options;}
  function dirty(){return !!(current?.active?.()!==false&&current?.dirty?.());}
  function confirmLeave(){
    const dialog=root.document?.getElementById('navigation-confirm');
    if(!dialog)return Promise.resolve(root.confirm('当前有尚未保存的修改。离开后这些修改会丢失，是否继续离开？'));
    const trigger=root.document.activeElement;
    return new Promise(resolve=>{
      dialog.oncancel=event=>{event.preventDefault();dialog.close('stay');};
      dialog.onclose=()=>{const approved=dialog.returnValue==='leave';if(!approved&&trigger?.isConnected)trigger.focus({preventScroll:true});resolve(approved);};
      dialog.querySelector('[data-navigation-stay]').onclick=()=>dialog.close('stay');
      dialog.querySelector('[data-navigation-leave]').onclick=()=>dialog.close('leave');
      dialog.showModal();dialog.querySelector('[data-navigation-stay]').focus();
    });
  }
  async function leave(){
    if(leaving)return false;
    const guard=current;leaving=true;
    try{
      if(dirty()&&!await confirmLeave())return false;
      if(current!==guard)return false;
      current?.checkpoint?.();current=null;return true;
    }finally{leaving=false;}
  }
  async function confirmDiscard(){if(leaving)return false;leaving=true;try{return await confirmLeave();}finally{leaving=false;}}
  root.addEventListener?.('beforeunload',event=>{
    current?.checkpoint?.();
    if(dirty()){event.preventDefault();event.returnValue='';}
  });
  // Drafts live only in this page's memory. They are never written to storage.
  function forms(document,{selector='#content form',retainMissing=false}={}){
    const baselines=new Map(),drafts=new Map();
    const key=form=>form.dataset?.formKey||form.id||Object.entries(form.dataset||{}).map(([k,v])=>k+':'+v).join('|');
    const fields=form=>[...(form.elements||[])].filter(el=>el.name&&!['submit','button','file'].includes(el.type));
    const snapshot=form=>fields(form).map(el=>({name:el.name,type:el.type,value:el.value,checked:!!el.checked}));
    const all=()=>[...(document.querySelectorAll(selector)||[])];
    function capture(){for(const form of all()){const id=key(form),value=snapshot(form);if(!id)continue;if(!baselines.has(id))baselines.set(id,value);if(JSON.stringify(value)===JSON.stringify(baselines.get(id)))drafts.delete(id);else drafts.set(id,value);}}
    function restore(){const present=new Set();for(const form of all()){const id=key(form);if(!id)continue;present.add(id);if(!drafts.has(id))baselines.set(id,snapshot(form));const value=drafts.get(id);if(!value)continue;const controls=fields(form);for(let i=0;i<controls.length;i++){const saved=value[i],el=controls[i];if(saved?.name===el.name&&saved.type===el.type){el.value=saved.value;if(['checkbox','radio'].includes(el.type))el.checked=saved.checked;}}}if(!retainMissing)for(const id of baselines.keys())if(!present.has(id)){baselines.delete(id);drafts.delete(id);}}
    function savepoint(selector){const form=document.querySelector(selector);return form?{id:key(form),value:snapshot(form)}:null;}
    function saved(selector,point){const form=document.querySelector(selector),id=point?.id||(form&&key(form));if(id){baselines.set(id,point?.value||snapshot(form));drafts.delete(id);capture();}}
    function forget(selector){const form=document.querySelector(selector),id=form?key(form):selector.replace(/^#/,'');baselines.delete(id);drafts.delete(id);}
    return {capture,restore,saved,savepoint,forget,dirty(){capture();return drafts.size>0;}};
  }
  const memory=new Map(),key=user=>'campustrace:job-browse:v1:'+user;
  const text=(v,max=200)=>typeof v==='string'?v.slice(0,max):'';
  function clean(value={}){
    return {query:text(value.query),filter:['BASIC','ANALYZED','STALE','FAILED'].includes(value.filter)?value.filter:'',tier:['HIGH','POSSIBLE','UNCERTAIN','LOW'].includes(value.tier)?value.tier:'',workflow:['UNHANDLED','SAVED','PLANNED','APPLIED','IGNORED','ENDED'].includes(value.workflow)?value.workflow:'',direction:['MAIN','ALL','MATCH','RELATED','UNCERTAIN','UNRELATED'].includes(value.direction)?value.direction:'',showIgnored:value.showIgnored===true,sort:value.sort==='deep'?'deep':'local',company:text(value.company),city:text(value.city),onlySelected:value.onlySelected===true,page:Number.isInteger(value.page)&&value.page>0?Math.min(value.page,20000):1,lastJob:text(value.lastJob,128),lastTab:['overview','evidence','source','preparation'].includes(value.lastTab)?value.lastTab:'overview',drawerOpen:value.drawerOpen===true};
  }
  function readBrowse(user){try{const value=root.sessionStorage?.getItem(key(user));if(value)return clean(JSON.parse(value)||{});}catch{}return clean(memory.get(user));}
  function storeBrowse(user,value){const safe=clean(value);memory.set(user,safe);try{root.sessionStorage?.setItem(key(user),JSON.stringify(safe));}catch{}}
  const api={register,leave,confirmDiscard,dirty,forms,readBrowse,storeBrowse};if(typeof module==='object'&&module.exports)module.exports=api;root.CampusNavigation=api;return api;
})(typeof window==='undefined'?globalThis:window);
