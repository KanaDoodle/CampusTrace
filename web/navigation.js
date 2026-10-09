'use strict';
const CampusNavigation=(function(root){
  let current=null,leaving=false;
  function register(options){current=options;}
  function dirty(){return !!(current?.active?.()!==false&&current?.dirty?.());}
  function confirmLeave(){
    const dialog=root.document?.getElementById('navigation-confirm');
    if(!dialog)return Promise.resolve(root.confirm('当前有尚未保存的修改。未保留的修改会丢失，是否继续离开？'));
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
      current?.checkpoint?.();current?.dispose?.();current=null;return true;
    }finally{leaving=false;}
  }
  async function confirmDiscard(){if(leaving)return false;leaving=true;try{return await confirmLeave();}finally{leaving=false;}}
  root.addEventListener?.('beforeunload',event=>{
    current?.checkpoint?.();
    if(dirty()){event.preventDefault();event.returnValue='';}
  });
  // Form tracking is in memory. Optional persistence is handled by CampusDrafts.
  function forms(document,{selector='#content form',retainMissing=false,ignoreNames=[]}={}){
    const baselines=new Map(),drafts=new Map(),committed=new Set();
    const key=form=>form.dataset?.formKey||form.id||Object.entries(form.dataset||{}).map(([k,v])=>k+':'+v).join('|');
    const fields=form=>[...(form.elements||[])].filter(el=>el.name&&!ignoreNames.includes(el.name)&&!['submit','button','file'].includes(el.type));
    const snapshot=form=>fields(form).map(el=>({name:el.name,type:el.type,value:el.value,checked:!!el.checked}));
    const all=()=>[...(document.querySelectorAll(selector)||[])];
    function capture(){for(const form of all()){const id=key(form),value=snapshot(form);if(!id)continue;if(!baselines.has(id))baselines.set(id,value);if(JSON.stringify(value)===JSON.stringify(baselines.get(id)))drafts.delete(id);else drafts.set(id,value);}}
    function restore(){const present=new Set();for(const form of all()){const id=key(form);if(!id)continue;present.add(id);if(!drafts.has(id)||committed.has(id))baselines.set(id,snapshot(form));committed.delete(id);const value=drafts.get(id);if(!value)continue;const controls=fields(form);for(let i=0;i<controls.length;i++){const saved=value[i],el=controls[i];if(saved?.name===el.name&&saved.type===el.type){el.value=saved.value;if(['checkbox','radio'].includes(el.type))el.checked=saved.checked;}}}if(!retainMissing)for(const id of baselines.keys())if(!present.has(id)){baselines.delete(id);drafts.delete(id);}}
    function savepoint(selector){const form=document.querySelector(selector);return form?{id:key(form),value:snapshot(form)}:null;}
    function saved(selector,point){const form=document.querySelector(selector),id=point?.id||(form&&key(form));if(id){if(point)committed.add(id);baselines.set(id,point?.value||snapshot(form));drafts.delete(id);capture();}}
    function forget(selector){const form=document.querySelector(selector),id=form?key(form):selector.replace(/^#/,'');baselines.delete(id);drafts.delete(id);}
    function entries(allow=()=>true){capture();return [...drafts].filter(([id])=>allow(id)).map(([id,value])=>({id,baseline:baselines.get(id),value}));}
    function hydrate(records,prepare=()=>{}){
      let restored=0,conflicts=0,conflictIDs=[];
      for(const record of records){
        const form=all().find(form=>key(form)===record.id);
        if(!form||JSON.stringify(baselines.get(record.id))!==JSON.stringify(record.baseline)||JSON.stringify(snapshot(form))!==JSON.stringify(baselines.get(record.id))){conflicts++;conflictIDs.push(record.id);continue;}
        prepare(form,record.value);
        const controls=fields(form),seen=new Map();
        for(const el of controls){const name=el.name+'|'+el.type,index=seen.get(name)||0;seen.set(name,index+1);const value=record.value.filter(v=>v.name===el.name&&v.type===el.type)[index];if(value){el.value=value.value;if(['checkbox','radio'].includes(el.type))el.checked=value.checked;}}
        drafts.set(record.id,snapshot(form));restored++;
      }
      return {restored,conflicts,conflictIDs};
    }
    return {capture,restore,saved,savepoint,forget,entries,hydrate,dirty(){capture();return drafts.size>0;}};
  }
  const memory=new Map(),key=user=>'campustrace:job-browse:v1:'+user;
  const text=(v,max=200)=>typeof v==='string'?v.slice(0,max):'';
  function clean(value={}){
    return {radarView:value.radarView==='company'?'company':'list',query:text(value.query),queryMode:value.queryMode==='ANY'?'ANY':'ALL',excludeQuery:text(value.excludeQuery),cities:Array.isArray(value.cities)?[...new Set(value.cities.filter(v=>typeof v==='string').map(v=>text(v,80)))].filter(Boolean).slice(0,30):[],filter:['BASIC','ANALYZED','STALE','FAILED'].includes(value.filter)?value.filter:'',tier:['HIGH','POSSIBLE','UNCERTAIN','LOW'].includes(value.tier)?value.tier:'',workflow:['UNHANDLED','SAVED','PLANNED','APPLIED','IGNORED','ENDED'].includes(value.workflow)?value.workflow:'',direction:['MAIN','ALL','MATCH','RELATED','UNCERTAIN','UNRELATED'].includes(value.direction)?value.direction:'',showIgnored:value.showIgnored===true,sort:['deep','technical','local','updated','created','company'].includes(value.sort)?value.sort:'deep',sortOrder:value.sortOrder==='asc'?'asc':'desc',company:text(value.company),city:text(value.city),onlySelected:value.onlySelected===true,page:Number.isInteger(value.page)&&value.page>0?Math.min(value.page,20000):1,lastJob:text(value.lastJob,128),lastTab:['overview','evidence','source','preparation'].includes(value.lastTab)?value.lastTab:'overview',drawerOpen:value.drawerOpen===true};
  }
  function readBrowse(user){try{const value=root.sessionStorage?.getItem(key(user));if(value)return clean(JSON.parse(value)||{});}catch{}return clean(memory.get(user));}
  function storeBrowse(user,value){const safe=clean(value);memory.set(user,safe);try{root.sessionStorage?.setItem(key(user),JSON.stringify(safe));}catch{}}
  const presetMemory=new Map(),workflowMemory=new Map(),undoMemory=new Map();
  const presetKey=user=>'campustrace:filter-presets:v1:'+user;
  function readPresets(user){
    let values=presetMemory.get(user)||[];try{const raw=root.localStorage?.getItem(presetKey(user));if(raw)values=JSON.parse(raw);}catch{}
    return (Array.isArray(values)?values:[]).filter(v=>v&&typeof v.name==='string'&&v.name.trim()).slice(0,20).map(v=>({name:text(v.name.trim(),60),filters:clean(v.filters||{})}));
  }
  function savePreset(user,name,filters){
    name=text(String(name||'').trim(),60);if(!name)return false;
    const values=readPresets(user),index=values.findIndex(v=>v.name===name),item={name,filters:clean(filters)};
    // Presets describe filters, never a selection, detail drawer, or permission.
    item.filters.onlySelected=false;item.filters.page=1;item.filters.lastJob='';item.filters.drawerOpen=false;
    if(index>=0)values[index]=item;else if(values.length<20)values.push(item);else return false;
    presetMemory.set(user,values);try{root.localStorage?.setItem(presetKey(user),JSON.stringify(values));}catch{}return true;
  }
  function deletePreset(user,name){const values=readPresets(user).filter(v=>v.name!==name);presetMemory.set(user,values);try{root.localStorage?.setItem(presetKey(user),JSON.stringify(values));}catch{}}
  function cleanWorkflow(value={}){
    const page=n=>Number.isInteger(n)&&n>0?Math.min(n,20000):1;
    return {query:text(value.query),company:text(value.company),stage:text(value.stage,30),view:['all','pending','review','completed'].includes(value.view)?value.view:'all',openEnded:value.openEnded===true,ongoingPage:page(value.ongoingPage),endedPage:page(value.endedPage),page:page(value.page),scroll:Number.isFinite(value.scroll)?Math.max(0,Math.min(value.scroll,1e7)):0};
  }
  const workflowKey=(user,type)=>'campustrace:workflow-browse:v1:'+user+':'+type;
  function readWorkflow(user,type){const k=workflowKey(user,type);let value=workflowMemory.get(k)||{};try{const raw=root.sessionStorage?.getItem(k);if(raw)value=JSON.parse(raw);}catch{}return cleanWorkflow(value||{});}
  function storeWorkflow(user,type,value){const k=workflowKey(user,type),safe=cleanWorkflow(value);workflowMemory.set(k,safe);try{root.sessionStorage?.setItem(k,JSON.stringify(safe));}catch{}}
  function readUndo(user){let value=undoMemory.get(user)||[];try{const raw=root.sessionStorage?.getItem('campustrace:ignore-undo:v1:'+user);if(raw)value=JSON.parse(raw);}catch{}return Array.isArray(value)?[...new Set(value.filter(v=>typeof v==='string'&&v.length<=128))].slice(0,10000):[];}
  function storeUndo(user,ids){const safe=[...new Set(ids)].slice(0,10000);undoMemory.set(user,safe);try{root.sessionStorage?.setItem('campustrace:ignore-undo:v1:'+user,JSON.stringify(safe));}catch{}}
  const api={register,leave,confirmDiscard,dirty,forms,readBrowse,storeBrowse,readPresets,savePreset,deletePreset,readWorkflow,storeWorkflow,readUndo,storeUndo};if(typeof module==='object'&&module.exports)module.exports=api;root.CampusNavigation=api;return api;
})(typeof window==='undefined'?globalThis:window);
