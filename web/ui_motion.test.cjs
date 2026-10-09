const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),vm=require('node:vm');

function ui(root={}){
  vm.runInNewContext(fs.readFileSync(require.resolve('./ui.js'),'utf8'),{window:root});
  return root.CampusUI;
}

test('motion honors the current reduced-motion preference and cancels an earlier reveal',()=>{
  let reduced=false,cancelled=0;
  const calls=[],root={matchMedia:query=>{assert.equal(query,'(prefers-reduced-motion: reduce)');return {matches:reduced};}};
  const U=ui(root),el={animate(frames,options){calls.push({frames,options});return {cancel(){cancelled++;}};}};
  U.reveal(el);assert.equal(calls.length,1);assert.equal(calls[0].options.duration,160);
  assert.equal(calls[0].frames.at(-1).opacity,1);
  assert.equal(calls[0].options.fill,undefined); // No retained opacity or layout transform.
  reduced=true;U.reveal(el);
  assert.equal(cancelled,1);assert.equal(calls.length,1);
  reduced=false;U.reveal(el);assert.equal(calls.length,2);
});

test('older browsers can open content without a motion API',()=>{
  const U=ui();assert.doesNotThrow(()=>U.reveal(null));assert.doesNotThrow(()=>U.reveal({}));
});

test('a restored dialog keeps its reading position without replaying entrance motion',()=>{
  const attrs=new Set(),dialog={open:true,scrollTop:0,setAttribute:k=>attrs.add(k),removeAttribute:k=>attrs.delete(k),showModal(){this.open=true;}};
  const U=ui({document:{getElementById:()=>dialog},scrollTo(){}});
  U.restore({dialogs:[{id:'review',top:120}]});
  assert.equal(dialog.scrollTop,120);assert.ok(attrs.has('data-motion-quiet'));
  dialog.open=false;U.openDialog(dialog);assert.equal(dialog.open,true);assert.ok(!attrs.has('data-motion-quiet'));
});

test('tabs reveal only the newly visible panel and preserve keyboard focus semantics',()=>{
  let reveals=0,focused=0;
  const panels={a:{hidden:false},b:{hidden:true,animate(){reveals++;return {cancel(){}};}}};
  const tabs=['a','b'].map(id=>({attrs:{'aria-controls':id},getAttribute(k){return this.attrs[k];},setAttribute(k,v){this.attrs[k]=v;},closest(){return {querySelectorAll:()=>tabs};},focus(options){assert.equal(options.preventScroll,true);focused++;}}));
  const U=ui({document:{getElementById:id=>panels[id]}});
  U.activateTab(tabs[1]);assert.equal(reveals,1);assert.equal(panels.a.hidden,true);assert.equal(panels.b.hidden,false);
  assert.equal(tabs[1].tabIndex,0);assert.equal(tabs[0].tabIndex,-1);assert.equal(tabs[1].attrs['aria-selected'],'true');
  U.activateTab(tabs[1]);assert.equal(reveals,1);assert.equal(focused,2);
});
