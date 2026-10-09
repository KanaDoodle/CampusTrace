'use strict';
(function(root){
 const loaded=new Map();
 const shared=['models.js','knowledge.js','applications.js','campaigns.js','matching_chat.js','matching_decision.js','matching_tasks.js','inventory.js','matching.js'];
 const routes={
  matching:shared,
  company:[...shared,'company_chat.js','company_decision.js','company_decision.css'],
  profile:['models.js','profile_local.js','profile_education.js','profile.js','drafts.js'],
  evidence:['profile_local.js','evidence.js'],
  models:['models.js','knowledge.js','knowledge.css'],
  knowledge:['models.js','matching.js','knowledge.js','knowledge_bulk.js','knowledge.css','agent.css'],
  agent:[...shared,'agent_actions.js','agent_workspace.js','agent_harness.js','agent.css'],
  practice:['practice.js'],
  applications:['campaigns.js','applications.js'],
  interviews:['interviews.js','drafts.js'],
  radar:['todos.js','source_catalog.js','source_bulk.js','radar.js'],
  zip:['vendor/jszip.min.js'],drafts:['drafts.js']
 };
 function asset(path){
  if(loaded.has(path))return loaded.get(path);
  const promise=new Promise((resolve,reject)=>{
   const css=path.endsWith('.css'),el=root.document.createElement(css?'link':'script');
   if(css){el.rel='stylesheet';el.href='/'+path;}else{el.src='/'+path;el.async=true;}
   el.onload=()=>resolve();el.onerror=()=>{loaded.delete(path);el.remove();reject(Error('页面组件未能读取，请重试。'));};
   root.document.head.append(el);
  });
  loaded.set(path,promise);return promise;
 }
 async function ensure(route){if(!routes[route])return;await Promise.all(routes[route].map(asset));}
 const api={ensure,routes};root.CampusAssets=api;if(typeof module==='object'&&module.exports)module.exports=api;
})(typeof window==='undefined'?globalThis:window);
