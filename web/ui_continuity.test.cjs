'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');

class Element {
 constructor(tag,attrs={},children=[]){this.nodeName=tag.toUpperCase();this.nodeType=1;this.attrs=new Map(Object.entries(attrs));this.childNodes=[];for(const child of children)this.append(child);}
 get id(){return this.getAttribute('id')||'';}get attributes(){return [...this.attrs].map(([name,value])=>({name,value}));}
 get firstChild(){return this.childNodes[0]||null;}get nextSibling(){const nodes=this.parentNode?.childNodes||[];return nodes[nodes.indexOf(this)+1]||null;}
 get textContent(){return this.childNodes.map(n=>n.textContent).join('');}set textContent(value){this.childNodes=[];this.append(text(value));}
 get type(){return this.getAttribute('type')||'text';}get value(){return this.inputValue??this.getAttribute('value')??'';}set value(value){this.inputValue=value;}
 get checked(){return this.inputChecked??this.hasAttribute('checked');}set checked(value){this.inputChecked=value;}
 getAttribute(name){return this.attrs.get(name)??null;}hasAttribute(name){return this.attrs.has(name);}setAttribute(name,value){this.attrs.set(name,String(value));}removeAttribute(name){this.attrs.delete(name);}
 append(node){this.insertBefore(node,null);}insertBefore(node,before){node.parentNode?.removeChild(node);const index=before?this.childNodes.indexOf(before):this.childNodes.length;this.childNodes.splice(index,0,node);node.parentNode=this;}
 removeChild(node){this.childNodes.splice(this.childNodes.indexOf(node),1);node.parentNode=null;}
 cloneNode(deep){return new Element(this.nodeName,Object.fromEntries(this.attrs),deep?this.childNodes.map(n=>n.cloneNode(true)):[]);}
 querySelector(selector){return this.childNodes.find(n=>n.nodeType===1&&n.hasAttribute(selector.slice(1,-1)))||null;}
}
function text(value){return {nodeName:'#text',nodeType:3,nodeValue:value,get textContent(){return this.nodeValue;},get nextSibling(){const all=this.parentNode.childNodes;return all[all.indexOf(this)+1]||null;},cloneNode(){return text(this.nodeValue);}};}
const el=(tag,id,children=[])=>new Element(tag,id?{id}:{},children);
function setup(){
 let incoming;const document={createElement(tag){if(tag!=='template')return new Element(tag);return {get content(){return incoming;},set innerHTML(value){}};}};
 const root={document};vm.runInNewContext(fs.readFileSync(__dirname+'/ui.js','utf8'),{window:root});
 return {U:root.CampusUI,patch(host,tree){incoming=el('fragment','',tree);root.CampusUI.reconcile(host,'trusted markup');}};
}
test('partial updates retain search nodes, unsaved typing and unchanged job cards',()=>{
 const h=setup(),input=new Element('input',{id:'search',value:''}),card=el('article','job-a',[text('原说明')]),host=el('main','',[el('form','filters',[input]),el('section','jobs',[card])]);
 input.value='正在输入中文';input.selectionStart=3;
 h.patch(host,[el('form','filters',[new Element('input',{id:'search',value:''})]),el('section','jobs',[el('article','job-a',[text('新说明')]),el('article','job-b',[text('新增')])])]);
 assert.equal(host.firstChild.firstChild,input);assert.equal(input.value,'正在输入中文');assert.equal(input.selectionStart,3);
 assert.equal(host.childNodes[1].firstChild,card);assert.equal(card.textContent,'新说明');assert.equal(host.childNodes[1].childNodes.length,2);
});
test('keyed updates reorder existing rows, remove absent rows and synchronize explicit selections',()=>{
 const h=setup(),a=el('article','a'),b=el('article','b'),old=el('article','old'),host=el('main','',[a,b,old]);
 h.patch(host,[el('article','b',[new Element('input',{id:'selected',type:'checkbox',checked:''})]),el('article','a')]);
 assert.deepEqual(host.childNodes,[b,a]);assert.equal(old.parentNode,null);assert.equal(b.firstChild.checked,true);
 h.patch(host,[el('article','b',[new Element('input',{id:'selected',type:'checkbox'})]),el('article','a')]);assert.equal(b.firstChild.checked,false);
});
test('a live dialog remains open through a refresh and form status stays inside its form',()=>{
 const h=setup(),dialog=new Element('dialog',{id:'review',open:''},[text('旧资料')]),host=el('main','',[dialog]);
 h.patch(host,[el('dialog','review',[text('当前资料')])]);assert.equal(host.firstChild,dialog);assert.equal(dialog.hasAttribute('open'),true);
 const form=el('form','edit');h.U.formStatus(form,'保存失败','error');assert.equal(form.firstChild.textContent,'保存失败');assert.equal(form.firstChild.getAttribute('role'),'alert');
 h.U.formStatus(form,'已保存','success');assert.equal(form.childNodes.length,1);assert.equal(form.firstChild.textContent,'已保存');assert.equal(form.firstChild.getAttribute('role'),'status');
});

function actionFixture(){
 const form={id:'edit',elements:[],querySelectorAll:()=>[]},notice={textContent:''},messages=[],failures=[];
 let currentForm=form;
 const context={pageVersion:1,authVersion:1,Error,FormData:class{},$:selector=>selector==='#notice'?notice:currentForm,CampusUI:{formStatus:(target,message,kind)=>messages.push({target,message,kind})},fail:error=>failures.push(error.message)};
 const source=fs.readFileSync(__dirname+'/app.js','utf8');vm.createContext(context);vm.runInContext(source.slice(source.indexOf('function formAction('),source.indexOf('\nfunction show()')),context);
 return {context,form,messages,failures,replace:form=>currentForm=form,bind:action=>context.formAction('#edit',action,{success:'已保存'})};
}
test('form feedback follows the current form after a save, and old-page failures stay out of the next page',async()=>{
 const h=actionFixture(),replacement={id:'edit'};h.bind(async()=>h.replace(replacement));await h.form.onsubmit({preventDefault(){},target:h.form});
 assert.equal(h.messages.at(-1).target,replacement);assert.equal(h.messages.at(-1).message,'已保存');
 const next=actionFixture();let reject;next.bind(()=>new Promise((_resolve,fail)=>reject=fail));const saving=next.form.onsubmit({preventDefault(){},target:next.form});next.context.pageVersion++;
 reject(Error('旧页面失败'));await saving;assert.equal(next.messages.length,1);assert.equal(next.failures.length,0);
});
