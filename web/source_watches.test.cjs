const {test}=require('node:test'),assert=require('node:assert/strict'),W=require('./source_watches.js');
const esc=v=>String(v??'').replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;');
const sources=[{id:'a',name:'北森<script>',adapter:'beisen_hr',minimum_interval:1800},{id:'b',name:'SAP',adapter:'sap',minimum_interval:1800}];
const watches=[{id:'one',source_id:'a',enabled:true,last_outcome:'SUCCESS',keyword:'Go & 后端'},{id:'two',source_id:'b',enabled:false,last_outcome:'RESTORED_PAUSED',priority:true},{id:'three',source_id:'a',enabled:true,last_outcome:'PARTIAL_FAILURE'}];
test('filters find failures, restored-paused watches and priorities without treating a pause as success',()=>{
 const ids=state=>watches.filter(w=>W.matches(w,sources.find(s=>s.id===w.source_id),state)).map(w=>w.id);
 assert.deepEqual(ids({status:'ATTENTION'}),['two','three']);assert.deepEqual(ids({status:'PAUSED'}),['two']);assert.deepEqual(ids({status:'ENABLED'}),['one','three']);assert.deepEqual(ids({search:' sap ',status:'PRIORITY'}),['two']);assert.deepEqual(ids({search:'没有'}),[]);
});
test('constraints come from API metadata, with catalog fallback for an older server',()=>{
 assert.equal(W.minimum(sources[0]),30);assert.equal(W.minimum({adapter:'new-source'},[{adapter:'new-source',minimum_interval:1800}]),30);assert.equal(W.minimum({minimum_interval:3600}),60);assert.equal(W.minimum({adapter:'custom'}),5);
});
test('compact cards prioritize failures, keep settings closed, and escape company and search data',()=>{
 const html=W.render(watches,sources,[null,null,{expected:50,completed:30,failed:2}],{esc,D:{date:()=> '今天'},form:w=>`<input value="${esc(w.keyword||'')}">`,state:{search:'"<img>',open:new Set(['three'])}});
 assert.ok(html.indexOf('data-watch-id="two"')<html.indexOf('data-watch-id="one"'));assert.match(html,/需处理<span>2/);assert.match(html,/30\/50，失败 2/);assert.match(html,/data-watch-settings="three" open/);assert.doesNotMatch(html,/data-watch-settings="one" open/);assert.match(html,/&lt;script&gt;/);assert.doesNotMatch(html,/<script>|<img>/);assert.match(html,/Go &amp; 后端/);
});
test('priority sorts ahead of normal sources when the backend omits false fields',()=>{
 const html=W.render([{id:'normal',source_id:'a',enabled:true},{id:'priority',source_id:'b',enabled:true,priority:true}],sources,[],{esc,D:{date:()=>''},form:()=>''});
 assert.ok(html.indexOf('data-watch-id="priority"')<html.indexOf('data-watch-id="normal"'));
});
test('filtering keeps form objects and unfinished settings intact; clearing restores all cards',()=>{
 const cards=watches.map(w=>({dataset:{watchId:w.id},hidden:false,input:{value:'未保存的设置'}}));
 const buttons=['ALL','ENABLED','ATTENTION','PAUSED','PRIORITY'].map(status=>({dataset:{watchFilter:status},setAttribute(k,v){this[k]=v;}})),search={value:'',focus(){this.focused=true;}},count={},empty={},clear={},settings={dataset:{watchSettings:'one'},open:true};
 const host={querySelector:s=>({'[data-watch-search]':search,'[data-watch-count]':count,'[data-watch-empty]':empty,'[data-watch-clear]':clear})[s],querySelectorAll:s=>s==='[data-watch-id]'?cards:s==='[data-watch-filter]'?buttons:[settings]};
 const state={};W.mount(host,watches,sources,state);assert.equal(count.textContent,'显示 3 / 3 个关注');buttons[2].onclick();assert.deepEqual(cards.map(v=>v.hidden),[true,false,false]);assert.equal(buttons[2]['aria-pressed'],'true');settings.ontoggle();assert.equal(state.open.has('one'),true);
 search.value='不存在';search.oninput();assert.equal(empty.hidden,false);assert.equal(count.textContent,'显示 0 / 3 个关注');assert.equal(cards[0].input.value,'未保存的设置');clear.onclick();assert.equal(search.value,'');assert.equal(search.focused,true);assert.deepEqual(cards.map(v=>v.hidden),[false,false,false]);assert.equal(settings.open,true);
});
test('late progress updates change only the matching watch and leave edited forms intact',()=>{
 const spans=['one','three'].map(id=>({dataset:{watchProgress:id},textContent:''})),form={value:'尚未保存'};
 const host={querySelectorAll:()=>spans,form};W.progress(host,'three',{expected:50,completed:30,failed:2});
 assert.equal(spans[0].textContent,'');assert.equal(spans[1].textContent,' · 已处理 30/50，失败 2');assert.equal(form.value,'尚未保存');W.progress(host,'missing',{expected:2});assert.equal(spans[1].textContent,' · 已处理 30/50，失败 2');
});

test('watch search commits Chinese composition only when complete and signals deferred page refresh',()=>{const search={value:''},count={},state={};let ended=0;const host={querySelector:s=>s==='[data-watch-search]'?search:s==='[data-watch-count]'?count:null,querySelectorAll:()=>[]};W.mount(host,watches,sources,state,{onCompositionEnd:()=>ended++});search.oncompositionstart();search.value='北森';search.oninput({isComposing:true});assert.equal(state.composing,true);assert.equal(state.search,undefined);search.oncompositionend();assert.equal(state.search,'北森');assert.equal(state.composing,false);assert.equal(ended,1);});
