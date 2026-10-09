'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict');
const C=require('./company_chat.js');
const doc={version:C.version,prompt_revision:'revision',candidate_hash:'c'.repeat(64),company:'测试公司',input_key:'i'.repeat(64),job_ids:['a','b'],summary:'整体建议',choices:[{job_id:'b',rank:1,reason:'核心工作相关',advantage:'有可迁移实践',tradeoff:'领域待确认',job_excerpt:'原文',evidence:[]},{job_id:'a',rank:2,reason:'可以备选',advantage:'基础相关',tradeoff:'准备成本更大',job_excerpt:'原文',evidence:[]}],questions:['长期行业意愿？']};
test('company package includes full narratives and a standalone schema without prior ranking or truncation',()=>{
 const text='完整岗位职责'+ '原文'.repeat(18000),pack={document:doc,candidate:{document:'本人核对过的完整求职资料'},jobs:[{job_id:'a',text},{job_id:'b',text:'另一完整 JD'}],prompt:'使用开放式语言列表，不把培养职责当成必需经验。',exported_at:'2026-10-09T06:00:00Z'};
 const file=C.makeFile(pack);assert.match(file.name,/CampusTrace-公司比较-.+\.md/);assert.ok(file.text.includes(text));assert.ok(file.text.includes(pack.prompt));assert.match(file.text,/不必生成单岗 assessment/);assert.match(file.text,/模板展示顺序不是推荐顺序/);assert.match(file.text,/确认保存/);assert.match(file.text,/不改已有单岗分析与人工评测标注/);
});
test('company import accepts JSON and one fenced reply and rejects other workflows or multiple replies',()=>{
 const raw=JSON.stringify(doc);assert.deepEqual(C.parseDocument('\ufeff'+raw),doc);assert.deepEqual(C.parseDocument('分析说明\n```json\n'+raw+'\n```'),doc);
 for(const source of ['{}','不完整 JSON',JSON.stringify({...doc,version:'campustrace-chat-v4'}),'```json\n'+raw+'\n```\n```json\n'+raw+'\n```','x'.repeat(2*1024*1024+1)])assert.throws(()=>C.parseDocument(source));
});
test('company preview sorts by rank and escapes all returned model text',()=>{
 const esc=v=>String(v).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;');
 const report=structuredClone(doc);report.summary='<script>bad()</script>';report.choices.reverse();
 const html=C.renderPreview({report,jobs:[{job_id:'a',title:'备选岗位'},{job_id:'b',title:'首选岗位'}],replaces:true},esc);
 assert.ok(html.indexOf('首选岗位')<html.indexOf('备选岗位'));assert.match(html,/&lt;script&gt;/);assert.doesNotMatch(html,/<script>/);assert.match(html,/确认保存公司比较/);assert.match(html,/单岗分析与人工标注保持不变/);
});
