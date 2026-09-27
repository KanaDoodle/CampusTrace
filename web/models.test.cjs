const {test}=require('node:test');
const assert=require('node:assert/strict');
const saved=new Map();
global.localStorage={
  getItem:key=>saved.get(key)??null,
  setItem:(key,value)=>saved.set(key,String(value)),
  removeItem:key=>saved.delete(key)
};
const models=require('./models.js');

test('presets derive fixed endpoints while provider keys persist for the same account',()=>{
  models.lock();saved.clear();
  models.bindUser('alice');
  assert.equal(models.available({model_available:false}),false);
  assert.throws(()=>models.select('gpt-6-luna'),/先填写/);
  models.saveKey('openai','openai-secret');
  assert.deepEqual(models.requestConfig(),{
    url:'https://api.openai.com/v1/chat/completions',model:'gpt-6-luna',api_key:'openai-secret'
  });
  models.select('gpt-6-sol');
  assert.equal(models.requestConfig().model,'gpt-6-sol');
  assert.equal(models.label({model_available:false}).includes('openai-secret'),false);
  models.lock();
  assert.equal(models.requestConfig(),undefined);
  models.bindUser('alice');
  assert.equal(models.requestConfig().model,'gpt-6-sol');
  assert.equal(models.requestConfig().api_key,'openai-secret');
  models.removeKey('openai');
  assert.equal(models.requestConfig(),undefined);
  assert.equal(saved.get('campustrace:model-keys:v1:alice').includes('openai-secret'),false);
});

test('DeepSeek key and model choice stay isolated from other accounts',()=>{
  models.lock();saved.clear();
  models.bindUser('alice');
  models.saveKey('deepseek','deepseek-secret');
  models.select('deepseek-v4-pro');
  assert.deepEqual(models.requestConfig(),{
    url:'https://api.deepseek.com/chat/completions',model:'deepseek-v4-pro',api_key:'deepseek-secret'
  });
  models.bindUser('bob');
  assert.equal(models.requestConfig(),undefined);
  assert.equal(models.available({model_available:true}),true);
  assert.equal(models.sessionID({model_available:true}),'web-default');
  models.saveKey('openai','bob-secret');
  models.bindUser('alice');
  assert.equal(models.requestConfig().api_key,'deepseek-secret');
  assert.equal(models.sessionID(),'web-deepseek-v4-pro');
});

test('invalid keys, providers and preset IDs are rejected',()=>{
  models.lock();saved.clear();
  assert.throws(()=>models.bindUser('../alice'),/当前账号/);
  models.bindUser('alice');
  for(const key of ['', 'a\nb', 'x'.repeat(1025)])assert.throws(()=>models.saveKey('openai',key),/API 密钥/);
  assert.throws(()=>models.saveKey('other','secret'),/未知/);
  assert.throws(()=>models.select('arbitrary-model'),/不存在/);
  assert.throws(()=>models.removeKey('other'),/未知/);
  assert.equal(saved.size,0);
});

test('unavailable local storage leaves non-model pages usable',()=>{
  const original=global.localStorage;
  models.lock();global.localStorage={getItem:()=>{throw new Error('blocked');},setItem:()=>{throw new Error('blocked');}};
  try{
    models.bindUser('alice');
    assert.equal(models.requestConfig(),undefined);
    assert.throws(()=>models.saveKey('openai','secret'),/无法在此浏览器保存/);
  }finally{global.localStorage=original;models.lock();}
});
