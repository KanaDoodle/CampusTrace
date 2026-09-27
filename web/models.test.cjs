const {test}=require('node:test');
const assert=require('node:assert/strict');
const models=require('./models.js');

test('models can be selected for one page and are cleared on logout',()=>{
  models.clear();
  assert.equal(models.available({model_available:false}),false);
  const id=models.add({name:'私人模型',url:'https://api.example.com/v1/chat/completions',model:'model-1',api_key:'top-secret'});
  assert.equal(models.available({model_available:false}),true);
  assert.deepEqual(models.requestConfig(),{url:'https://api.example.com/v1/chat/completions',model:'model-1',api_key:'top-secret'});
  assert.equal(models.label({model_available:false}).includes('top-secret'),false);
  assert.equal(models.sessionID(),'web-'+id);
  models.select('default');
  assert.equal(models.requestConfig(),undefined);
  assert.equal(models.sessionID({model_available:true}),'web-default');
  assert.equal(models.sessionID({model_available:false}),'web-demo');
  assert.equal(models.label({model_available:true,model:'server-model'}),'服务器默认 · server-model');
  models.select(id);
  models.clear();
  assert.equal(models.requestConfig(),undefined);
  assert.equal(models.available({model_available:false}),false);
});

test('unsafe or incomplete model settings are rejected',()=>{
  models.clear();
  for(const url of ['http://api.example.com/v1/chat/completions','https://localhost/v1/chat/completions','https://private.local/v1/chat/completions','https://10.0.0.1/v1/chat/completions','https://api.example.com:8443/v1/chat/completions','https://user:pass@api.example.com/v1/chat/completions','https://api.example.com/v1/chat/completions?key=value']){
    assert.throws(()=>models.add({name:'bad',url,model:'model',api_key:'key'}));
  }
  assert.equal(models.requestConfig(),undefined);
});
