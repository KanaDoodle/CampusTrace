'use strict';
(function(root){
 // This state belongs to one mounted page and is never written to storage.
 function create({api,active=()=>true,changed=()=>{}}){
  let key='',revision=0,state=null;
  function ensure({scope,text='',mode='ALL',exclude=''}){
   if(!text.trim()&&!exclude.trim()){key='';revision++;state=null;return null;}
   const next=JSON.stringify([scope,text,mode,exclude]);if(next===key)return state;
   key=next;const version=++revision;state={pending:true,ids:null,mode:'',more:false,error:false};
   Promise.resolve().then(()=>api('/api/jobs/search','POST',{text,mode,exclude,limit:1000})).then(result=>{
    if(version!==revision||!active())return;
    if(!Array.isArray(result.job_ids)||!['fulltext','basic'].includes(result.mode))throw Error('search response');
    state={pending:false,ids:new Set(result.job_ids),mode:result.mode,more:!!result.more,error:false};changed();
   }).catch(()=>{if(version!==revision||!active())return;state={pending:false,ids:null,mode:'',more:false,error:true};changed();});
   return state;
  }
  function retry(){if(!state?.error)return false;key='';revision++;state=null;return true;}
  return {ensure,retry};
 }
 const api={create};root.CampusJobSearch=api;if(typeof module==='object'&&module.exports)module.exports=api;
})(typeof window==='undefined'?globalThis:window);
