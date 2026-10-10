const {test}=require('node:test');
const assert=require('node:assert/strict');
const M=require('./matching.js'),U=require('./ui.js'),D=require('./display.js');
const helpers={esc:U.esc,D,U};
test('inline detail survives list replacement, rejects stale loads and keeps opening from moving the page',()=>{
  const vm=require('node:vm'),fs=require('node:fs');
  const classes=()=>({values:new Set(),add(v){this.values.add(v);},toggle(v,on){if(on)this.values.add(v);else this.values.delete(v);}});
  const parent=id=>({id,classList:classes(),append(el){el.parentElement=this;}});
  let host=parent('first-list'),shows=0;
  const drawer={open:false,classList:classes(),show(){this.open=true;shows++;root.scrollY=900;},close(){this.open=false;}};
  const root={scrollX:0,scrollY:20,scrollTo(v){this.scrollX=v.left;this.scrollY=v.top;},document:{body:parent('body'),activeElement:null,querySelector(s){return s==='#job-drawer'?drawer:s==='#match-detail'?host:null;},querySelectorAll(){return drawer.open?[drawer]:[];}}};
  const context=vm.createContext({window:root});vm.runInContext(fs.readFileSync(require.resolve('./ui.js'),'utf8'),context);const ui=root.CampusUI;
  const request=ui.drawer('loading first job');assert.equal(root.scrollY,20);assert.equal(drawer.parentElement,host);assert.equal(shows,1);
  ui.releaseDrawer();host=parent('replacement-list');ui.placeDrawer(host);
  assert.equal(drawer.parentElement,host);assert.equal(drawer.innerHTML,'loading first job');assert.ok(ui.drawerCurrent(request.revision));assert.equal(shows,1);
  const next=ui.drawer('loading next job');assert.ok(!ui.drawerCurrent(request.revision));assert.ok(ui.drawerCurrent(next.revision));
  ui.closeAll();assert.equal(drawer.parentElement,root.document.body);assert.ok(!ui.drawerCurrent(next.revision));
});
const base={job:{id:'job',title:'后端研发',company:'测试公司',locations:['上海']},state:'BASIC',local:{tier:'HIGH',score:90,direction:{status:'MATCH'},reasons:['Go 项目线索']}};
test('workbench rows never present local or stale scores as current technical matching',()=>{
  for(const state of ['BASIC','STALE']){
    const html=M.jobRowHTML({...base,state,score:99,coverage:100},helpers,false,false,false,false);
    assert.doesNotMatch(html,/>99\.0</);assert.match(html,state==='STALE'?/分析待更新/:/待深度分析/);
  }
  const missing=M.jobRowHTML({...base,state:'ANALYZED',score:null,coverage:30},helpers,false,false,false,false);
  assert.match(missing,/资料待核对/);assert.doesNotMatch(missing,/>0\.0</);
  const escaped=M.jobRowHTML({...base,job:{...base.job,title:'<script>bad</script>'}},helpers,true,true,false,true);
  assert.match(escaped,/&lt;script&gt;/);assert.doesNotMatch(escaped,/<script>/);
});
test('browse views identify one common range without disguising combined advanced conditions',()=>{
  assert.equal(M.browseView('MAIN','',false),'MAIN');
  assert.equal(M.browseView('ALL','APPLIED',false),'APPLIED');
  assert.equal(M.browseView('MATCH','APPLIED',false),'CUSTOM');
  assert.equal(M.browseView('ALL','IGNORED',false),'CUSTOM');
  assert.equal(M.browseView('MATCH','APPLIED',true),'SELECTED');
  const html=M.browseViewsHTML({direction:'ALL',workflow:'PLANNED',onlySelected:false,hasDirection:true,selectedCount:3});
  assert.match(html,/id="match-view-PLANNED"[^>]*aria-pressed="true"/);
  assert.equal((html.match(/aria-pressed="true"/g)||[]).length,1);
  assert.match(html,/已选 3/);assert.match(html,/主投相关/);
  const custom=M.browseViewsHTML({direction:'UNCERTAIN',workflow:'',onlySelected:false,hasDirection:false,selectedCount:0});
  assert.doesNotMatch(custom,/aria-pressed="true"|match-view-MAIN|match-view-SELECTED/);
});
test('current overview separates technical strengths from qualifications and withdrawn evidence',()=>{
  const requirements=[{id:'q',category:'QUALIFICATION',text:'硕士学历',excerpt:'硕士'},{id:'soft',category:'REQUIRED',aspect:'SOFT',text:'沟通能力',excerpt:'沟通'},{id:'go',category:'REQUIRED',text:'Go 服务开发',excerpt:'Go'},{id:'wrong',category:'REQUIRED',text:'平台经验',excerpt:'平台'}];
  const result={score:null,coverage:40,model:'fixture',requirements,matches:[{requirement_id:'q',result:'DIRECT',evidence:[{excerpt:'学历证明'}]},{requirement_id:'soft',result:'DIRECT',evidence:[{excerpt:'沟通描述'}]},{requirement_id:'go',result:'DIRECT',evidence:[{excerpt:'实现 <Go> 接口'}]},{requirement_id:'wrong',result:'NO_EVIDENCE',review_note:'INVALID_ABILITY_EVIDENCE',evidence:[]}],qualifications:{status:'UNKNOWN',results:[]}};
  const html=M.overviewHTML({state:'ANALYZED',result},base,helpers),highlights=html.split('class="workbench-highlights"')[1].split('</section>')[0];
  assert.match(highlights,/实现 &lt;Go&gt; 接口/);assert.doesNotMatch(highlights,/学历证明|沟通描述/);
  assert.match(highlights,/引用已撤销/);assert.match(html,/资料待核对，可先查看相关经历/);
  const stale=M.overviewHTML({state:'STALE',result:{...result,score:100}},base,helpers);
  assert.doesNotMatch(stale,/核心技术匹配度 100/);assert.match(stale,/上次深度分析（已过期）/);
});
