'use strict';
const CampusUI=(function(root){
  const paths={
    briefcase:'<rect x="3" y="7" width="18" height="14" rx="2"/><path d="M8 7V5a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M3 12a26 26 0 0 0 18 0M10 12v3h4v-3"/>',
    radar:'<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="5"/><path d="m12 12 7-7"/>',
    list:'<rect x="4" y="3" width="16" height="18" rx="2"/><path d="m8 9 1 1 2-2m2 1h3m-8 6 1 1 2-2m2 1h3"/>',
    person:'<circle cx="12" cy="8" r="4"/><path d="M4 21v-2a8 8 0 0 1 16 0v2"/>',
    spark:'<path d="m12 3 2.7 6.3L21 12l-6.3 2.7L12 21l-2.7-6.3L3 12l6.3-2.7Z"/>',
    search:'<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>',
    filter:'<path d="M4 7h16M4 17h16"/><circle cx="9" cy="7" r="2"/><circle cx="15" cy="17" r="2"/>',
    close:'<path d="m6 6 12 12M6 18 18 6"/>',
    chevron:'<path d="m6 9 6 6 6-6"/>',
    check:'<path d="m5 12 4 4L19 6"/>',
    warning:'<path d="m12 3 10 18H2Z"/><path d="M12 9v5m0 3v.2"/>',
    shield:'<path d="m12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6Z"/><path d="m8 12 3 3 5-6"/>',
    upload:'<path d="M4 15v5h16v-5M12 16V3m-5 5 5-5 5 5"/>',
    download:'<path d="M4 16v4h16v-4M12 3v13m-5-5 5 5 5-5"/>',
    plus:'<path d="M12 5v14M5 12h14"/>',
    clock:'<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
    bell:'<path d="M5 17h14l-2-3V9a5 5 0 0 0-10 0v5Zm5 3h4"/>',
    bookmark:'<path d="M6 3h12v18l-6-4-6 4Z"/>',
    logout:'<path d="M9 4H4v16h5m-1-8h12m-5-5 5 5-5 5"/>',
    refresh:'<path d="M20 7v5h-5M4 17v-5h5M5 7a8 8 0 0 1 14-1l1 6M4 12l1 6a8 8 0 0 0 14-1"/>'
  };
  const esc=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const icon=name=>`<svg class="icon" viewBox="0 0 24 24" aria-hidden="true">${paths[name]||paths.briefcase}</svg>`;
  const heading=(title,subtitle='',actions='')=>`<div class="page-heading"><div><h1>${esc(title)}</h1>${subtitle?`<p>${esc(subtitle)}</p>`:''}</div>${actions?`<div class="heading-actions">${actions}</div>`:''}</div>`;
  const modalHead=(id,title,subtitle='')=>`<div class="modal-head"><div><h2 id="${esc(id)}">${esc(title)}</h2>${subtitle?`<p>${esc(subtitle)}</p>`:''}</div><button type="button" class="icon-btn" data-dialog-close aria-label="关闭弹窗">${icon('close')}</button></div>`;
  function capture(){
    const doc=root.document,a=doc?.activeElement;
    return {id:a?.id,select:a?.dataset?.matchSelect,position:a?.selectionStart,end:a?.selectionEnd,x:root.scrollX||0,y:root.scrollY||0,
      dialogs:[...(doc?.querySelectorAll('dialog[open]')||[])].map(el=>({id:el.id,top:el.scrollTop})),
      regions:[...(doc?.querySelectorAll('[data-scroll-region]')||[])].map(el=>({id:el.id,top:el.scrollTop})),
      details:[...(doc?.querySelectorAll('details[data-remember][open]')||[])].map(el=>el.id)};
  }
  function restore(state){
    if(!state)return;const doc=root.document;
    for(const id of state.details||[]){const el=doc?.getElementById?.(id);if(el)el.open=true;}
    const target=state.id?doc?.getElementById?.(state.id):state.select?[...(doc?.querySelectorAll('[data-match-select]')||[])].find(el=>el.dataset.matchSelect===state.select):null;
    if(target&&!target.disabled&&target.getClientRects?.().length){target.focus({preventScroll:true});if(typeof state.position==='number')try{target.setSelectionRange(state.position,state.end);}catch{}}
    for(const saved of state.dialogs||[]){const el=doc?.getElementById?.(saved.id);if(el?.open)el.scrollTop=saved.top;}
    for(const saved of state.regions||[]){const el=doc?.getElementById?.(saved.id);if(el)el.scrollTop=saved.top;}
    root.scrollTo?.({left:state.x,top:state.y,behavior:'instant'});
  }
  function openDialog(el,onClose){if(!el)return;if(onClose)el.onclose=onClose;if(!el.open)el.showModal();}
  let drawerRevision=0,drawerTrigger=null;
  function placeDrawer(host){
    const el=root.document?.querySelector('#job-drawer');if(!el)return;
    (host||root.document.body).append(el);
    el.classList.toggle('is-docked',!!host);
    host?.classList.toggle('has-detail',el.open);
  }
  // Keep the same dialog alive across list renders: loading detail requests and
  // preparation checklists belong to this element, not to a particular list DOM.
  function releaseDrawer(){placeDrawer(null);}
  function drawer(html){
    const el=root.document.querySelector('#job-drawer'),host=root.document.querySelector('#match-detail');
    if(!el.open)drawerTrigger=root.document.activeElement;el.innerHTML=html;
    if(host){const x=root.scrollX||0,y=root.scrollY||0;placeDrawer(host);if(!el.open)el.show();host.classList.add('has-detail');root.scrollTo?.({left:x,top:y,behavior:'instant'});}
    else openDialog(el);
    return {element:el,revision:++drawerRevision};
  }
  function drawerCurrent(revision){return root.document.querySelector('#job-drawer')?.open&&revision===drawerRevision;}
  function closeAll(){for(const el of root.document.querySelectorAll('dialog[open]'))el.close();releaseDrawer();drawerRevision++;}
  function activateTab(button){
    for(const tab of button.closest('[role="tablist"]').querySelectorAll('[role="tab"]')){const selected=tab===button;tab.setAttribute('aria-selected',String(selected));tab.tabIndex=selected?0:-1;const panel=root.document.getElementById(tab.getAttribute('aria-controls'));if(panel)panel.hidden=!selected;}
    button.focus({preventScroll:true});
  }
  function showDialogNotice(message){
    const dialog=[...root.document.querySelectorAll('dialog[open]')].at(-1);if(!dialog)return;
    let notice=dialog.querySelector('.modal-notice');
    if(!notice){notice=root.document.createElement('p');notice.className='modal-notice';notice.setAttribute('role','status');notice.setAttribute('aria-live','polite');(dialog.querySelector('.modal-body,.drawer-body')||dialog).prepend(notice);}
    notice.textContent=message;
  }
  function notify(message){const el=root.document.querySelector('#notice');if(el)el.textContent=message;showDialogNotice(message);}
  function init(){
    const doc=root.document;for(const el of doc.querySelectorAll('[data-icon]'))el.innerHTML=icon(el.dataset.icon);
    doc.addEventListener('click',event=>{const button=event.target.closest('button');if(!button||button.disabled)return;if(button.hasAttribute('data-dialog-close'))button.closest('dialog')?.close();if(button.hasAttribute('data-view-tab'))activateTab(button);if(button.id==='mobile-menu'){const sidebar=button.closest('.sidebar');const open=sidebar.classList.toggle('menu-open');button.setAttribute('aria-expanded',String(open));}});
    doc.addEventListener('keydown',event=>{
      if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key)||event.target.getAttribute('role')!=='tab')return;
      const tabs=[...event.target.closest('[role="tablist"]').querySelectorAll('[role="tab"]')].filter(tab=>!tab.disabled);if(!tabs.length)return;let i=tabs.indexOf(event.target);i=event.key==='Home'?0:event.key==='End'?tabs.length-1:(i+(event.key==='ArrowRight'?1:-1)+tabs.length)%tabs.length;event.preventDefault();tabs[i].click();
    });
    const message=doc.querySelector('#notice');if(message&&root.MutationObserver)new root.MutationObserver(()=>showDialogNotice(message.textContent)).observe(message,{childList:true,subtree:true,characterData:true});
    const el=doc.querySelector('#job-drawer');if(el)el.addEventListener('close',()=>{drawerRevision++;el.closest('#match-detail')?.classList.remove('has-detail');for(const row of doc.querySelectorAll('[data-workbench-row]'))row.classList.remove('is-current');for(const button of doc.querySelectorAll('[data-match-job]'))button.removeAttribute('aria-current');if(drawerTrigger?.isConnected)drawerTrigger.focus({preventScroll:true});drawerTrigger=null;});
  }
  const api={esc,icon,heading,modalHead,capture,restore,openDialog,placeDrawer,releaseDrawer,drawer,drawerCurrent,closeAll,activateTab,notify,init};
  if(typeof module==='object'&&module.exports)module.exports=api;root.CampusUI=api;return api;
})(typeof window==='undefined'?globalThis:window);
