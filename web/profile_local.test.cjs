'use strict';
const test=require('node:test');
const assert=require('node:assert/strict');
const P=require('./profile_local.js');

test('redacts common direct identifiers before a resume can be previewed',()=>{
  const input='姓名：张三\n手机号码：13812345678\n邮箱：zhang@example.com\n项目 https://github.com/a/b\nGo/Redis';
  const text=P.redact(input);
  assert.equal(P.hasDirectIdentifiers(text),false);
  assert.match(text,/Go\/Redis/);
  for(const secret of ['张三','13812345678','zhang@example.com','github.com/a/b'])assert.equal(text.includes(secret),false);
});

test('TXT extraction stays local and rejects unsupported or empty files',async()=>{
  const file=new File(['姓名：李四\n项目使用 Go'],'resume.txt',{type:'text/plain'});
  assert.equal(await P.readFile(file),'[已移除身份或联系信息]\n项目使用 Go');
  await assert.rejects(P.readFile(new File(['hello'],'resume.doc')),/支持 PDF、DOCX 和 TXT/);
  await assert.rejects(P.readFile(new File([''],'resume.txt')),/没有读到可用文字/);
});
