'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const D=require('./display.js');

const source=fs.readFileSync(__dirname+'/app.js','utf8');
const render=source.slice(source.indexOf('function renderAgent('),source.indexOf('async function detail('));
const context={D,esc:value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])),translated:()=>'<p>依据详情</p>',document:{createElement:()=>({append(){}})}};
vm.createContext(context);vm.runInContext(render,context);

test('求职问答展示有据回答并转义不可信内容',()=>{
  const box={innerHTML:'',append(){}};
  context.renderAgent({terminal_reason:'COMPLETED',answer:'<img src=x onerror=alert(1)>\n第二行',model_steps:2,executed_tool_count:1,grounded_observations:[]},box);
  assert.match(box.innerHTML,/agent-answer/);
  assert.match(box.innerHTML,/&lt;img/);
  assert.doesNotMatch(box.innerHTML,/<img src/);
  assert.match(box.innerHTML,/第二行/);
});
