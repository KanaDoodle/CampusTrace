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

test('masks common mobile formats and labeled names while keeping skills',()=>{
  for(const phone of ['13812345678','138 1234 5678','138-1234-5678','+86 138 1234 5678','0086-138-1234-5678']){
    const text=P.redact(`手机 ${phone}；技能 Go`);
    assert.equal(P.hasDirectIdentifiers(text),false,phone);
    assert.equal(text.includes('1234'),false,phone);
    assert.match(text,/技能 Go/);
  }
  const text=P.redact('姓名：王小明 手机：+86 138-1234-5678 技术栈 Go\nFull Name: Alice Zhang');
  assert.equal(P.hasDirectIdentifiers(text),false);
  assert.equal(text.includes('王小明'),false);
  assert.equal(text.includes('Alice Zhang'),false);
  assert.match(text,/技术栈 Go/);
});

test('unlabeled names can be masked locally without storing them',()=>{
  const input='张三\n张三使用 Go 开发项目';
  assert.equal(P.redact(input),input);
  assert.equal(P.maskAdditionalName(input,'张三'),'[已移除姓名]\n[已移除姓名]使用 Go 开发项目');
  assert.throws(()=>P.maskAdditionalName(input,'张'),/至少 2 个字/);
});

test('TXT extraction stays local and rejects unsupported or empty files',async()=>{
  const file=new File(['姓名：李四\n项目使用 Go'],'resume.txt',{type:'text/plain'});
  assert.equal(await P.readFile(file),'[已移除姓名]\n项目使用 Go');
  await assert.rejects(P.readFile(new File(['hello'],'resume.doc')),/支持 PDF、DOCX 和 TXT/);
  await assert.rejects(P.readFile(new File([''],'resume.txt')),/没有读到可用文字/);
});

test('reviewed text length follows the backend UTF-8 byte limit',()=>{
  assert.equal(P.reviewedTextBytes('Go'),2);
  assert.equal(P.reviewedTextBytes('后端'),6);
  assert.equal(P.reviewedTextBytes('后'.repeat(5334)),16002);
});
