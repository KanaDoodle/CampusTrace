'use strict';
(function(root){
 const MAX_FILES=64,MAX_BYTES=1000000,queues=new Map();
 const bytes=v=>new TextEncoder().encode(v).length;
 const labels={ready:'待导入',invalid:'无法导入',duplicate:'本批重复，已跳过',importing:'正在保存',imported:'已导入',reused:'已存在，已跳过',failed:'失败，可重试'};
 async function readFiles(files,previous=[]){
  if(previous.length+files.length>MAX_FILES)throw Error('每批最多 64 个文件，请先清空已完成的批次。');
  if(previous.reduce((n,v)=>n+bytes(v.text||''),0)+files.reduce((n,v)=>n+v.size,0)>MAX_BYTES)throw Error('本批文件总量最多 1 MB，请分批选择。');
  const rows=[...previous];
  for(const file of files){
   const row={name:file.name,title:file.name.replace(/\.(txt|md)$/i,''),size:file.size,text:'',selected:true,status:'ready',error:''};
   try{
    if(!/\.(txt|md)$/i.test(file.name))throw Error('仅支持 TXT、MD 文本文件。');
    if(file.size>60000)throw Error('正文超过 60,000 字节，请拆分文件。');
    if(bytes(row.title)>200||!row.title.trim())throw Error('文件名作为标题，最多 200 字节。');
    row.text=await file.text();
    if(!row.text.trim()||bytes(row.text)>60000||row.text.includes('\0'))throw Error('正文为空、过长或不是有效文本。');
    if(rows.some(v=>v.title===row.title&&v.text===row.text&&v.status!=='invalid')){row.status='duplicate';row.selected=false;}
   }catch(e){row.status='invalid';row.selected=false;row.error=e.message;row.text='';}
   rows.push(row);
  }
  if(rows.reduce((n,v)=>n+bytes(v.text),0)>MAX_BYTES)throw Error('读取后的正文超过 1 MB，请分批选择。');
  return rows;
 }
 async function run(queue,{api,active=()=>true,update=()=>{},retry=false}){
  if(queue.busy)return;queue.busy=true;queue.stop=false;
  try{
   for(const row of queue.rows){
    if(queue.stop||!active())break;
    if(!row.selected||!(retry?row.status==='failed':['ready','failed'].includes(row.status)))continue;
    row.status='importing';row.error='';update();
    try{const result=await api('/api/documents/import','POST',{title:row.title,text:row.text});row.status=result.reused?'reused':'imported';row.id=result.id;}
    catch(e){row.status='failed';row.error=e.message||'保存未完成，请重试。';}
    update();
   }
  }finally{queue.busy=false;update();}
 }
 function markup(){return '<details class="agent-panel knowledge-import" id="knowledge-bulk"><summary>批量导入 TXT / MD</summary><p class="meta">每批最多 64 份、共 1 MB；单份正文最多 60,000 字节。</p><label>选择多个文本文件<input id="knowledge-bulk-files" type="file" multiple accept=".txt,.md,text/plain,text/markdown"></label><div id="knowledge-bulk-rows"></div><div class="actions"><button type="button" class="btn btn-primary" id="knowledge-bulk-start">导入勾选材料</button><button type="button" class="btn" id="knowledge-bulk-retry">重试勾选失败项</button><button type="button" class="text-btn" id="knowledge-bulk-stop">停止后续导入</button><button type="button" class="text-btn" id="knowledge-bulk-clear">清空本批</button></div><p class="meta" id="knowledge-bulk-notice" role="status"></p></details>';}
 function mount({user,api,esc,active=()=>true,onSaved=async()=>{}}){
  let disposed=false,reading=false,message='';const alive=()=>!disposed&&active();
  const queue=queues.get(user)||{rows:[],busy:false,stop:false};queues.set(user,queue);
  const q=s=>root.document.querySelector(s);
  function draw(){if(!alive())return;
   q('#knowledge-bulk-rows').innerHTML=queue.rows.map((v,i)=>`<article class="knowledge-bulk-row"><label class="check"><input type="checkbox" data-bulk-select="${i}" ${v.selected?'checked':''} ${!['ready','failed'].includes(v.status)||queue.busy?'disabled':''}>${esc(v.name)}</label><span class="meta">${esc(labels[v.status])} · ${v.size} 字节</span>${v.error?`<p class="knowledge-warning">${esc(v.error)}</p>`:''}${v.text?`<details><summary>预览正文${v.text.length>1200?'（前 1,200 字）':''}</summary><pre>${esc(v.text.slice(0,1200))}</pre></details>`:''}</article>`).join('');
   for(const b of root.document.querySelectorAll('[data-bulk-select]'))b.onchange=()=>{queue.rows[Number(b.dataset.bulkSelect)].selected=b.checked;draw();};
   q('#knowledge-bulk-files').disabled=queue.busy||reading;
   q('#knowledge-bulk-start').disabled=queue.busy||reading||!queue.rows.some(v=>v.selected&&['ready','failed'].includes(v.status));
   q('#knowledge-bulk-retry').disabled=queue.busy||reading||!queue.rows.some(v=>v.selected&&v.status==='failed');
   q('#knowledge-bulk-stop').disabled=!queue.busy||queue.stop;
   q('#knowledge-bulk-clear').disabled=queue.busy||reading||!queue.rows.length;
   const count=s=>queue.rows.filter(v=>v.status===s).length;
   q('#knowledge-bulk-notice').textContent=message||(queue.rows.length?`已导入 ${count('imported')}，已跳过 ${count('reused')+count('duplicate')}，失败 ${count('failed')}。${queue.busy?' 正在逐份保存…':'同标题、同正文的已有材料会自动跳过。'}`:'选择文件后会显示预览；离开页面会停止后续导入，本批在本次登录期间保留。');
  }
  q('#knowledge-bulk-files').onchange=async function(){const files=[...(this.files||[])];this.value='';if(!files.length)return;reading=true;message='正在读取本机文件…';draw();try{const rows=await readFiles(files,queue.rows);if(!alive())return;queue.rows=rows;message='';q('#knowledge-bulk').open=true;}catch(e){message=e.message;}finally{reading=false;draw();}};
  queue.update=draw;
  const start=async retry=>{message='';await run(queue,{api,active:alive,update:()=>queue.update?.(),retry});if(!alive())return;try{await onSaved();}catch{message='材料保存状态已保留，但列表刷新失败，请点击刷新。';draw();}};
  q('#knowledge-bulk-start').onclick=()=>start(false);q('#knowledge-bulk-retry').onclick=()=>start(true);
  q('#knowledge-bulk-stop').onclick=()=>{queue.stop=true;message='已停止后续导入，正在保存的这一份会保留结果。';draw();};
  q('#knowledge-bulk-clear').onclick=()=>{queue.rows=[];message='';draw();};
  if(queue.rows.length)q('#knowledge-bulk').open=true;draw();return {dispose(){disposed=true;queue.stop=true;if(queue.update===draw)queue.update=null;}};
 }
 const api={markup,mount,readFiles,run,lock(){for(const queue of queues.values())queue.stop=true;queues.clear();}};root.CampusKnowledgeBulk=api;if(typeof module==='object'&&module.exports)module.exports=api;
})(typeof window==='undefined'?globalThis:window);
