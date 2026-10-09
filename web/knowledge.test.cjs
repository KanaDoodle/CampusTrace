const {test}=require('node:test');
const assert=require('node:assert/strict');
const saved=new Map();global.localStorage={getItem:k=>saved.get(k)||null,setItem:(k,v)=>saved.set(k,v)};
const K=require('./knowledge.js');
test('retrieval keys and activation remain account scoped and save never invokes a provider',()=>{
 saved.clear();K.lock();assert.deepEqual(K.requestOptions(),{embedding:undefined,rerank:undefined,mask_name:''});K.bindUser('alice');
 K.save({embedding:{...K.defaults.embedding,api_key:'alice-secret'},enabled:true});
 assert.equal(K.requestOptions().embedding.api_key,'alice-secret');assert.equal(K.requestOptions().rerank,undefined);
 K.bindUser('bob');assert.equal(K.requestOptions().embedding,undefined);K.save({embedding:{...K.defaults.embedding,api_key:'bob-secret'},enabled:true});
 K.bindUser('alice');assert.equal(K.requestOptions().embedding.api_key,'alice-secret');assert.equal(K.settingsMarkup(v=>v).includes('alice-secret'),false);
 K.save({});assert.equal(K.requestOptions().embedding,undefined);assert.equal(saved.get('campustrace:retrieval:v1:alice').includes('alice-secret'),false);K.lock();
});
test('retrieval config rejects internal URLs, control characters and absent keys',()=>{
 K.bindUser('test');assert.throws(()=>K.save({enabled:true,embedding:{...K.defaults.embedding,api_key:'bad\nkey'}}),/有效/);
 for(const url of ['http://127.0.0.1/embeddings','https://127.0.0.1/embeddings','https://localhost/embeddings','https://api.internal/v1/embeddings','https://example.com:8080/embeddings','https://u:p@example.com/v1/embeddings','https://example.com/v1/embeddings?secret=x'])assert.equal(K.validEndpoint(url),false);
 assert.equal(K.validEndpoint(K.defaults.embedding.url),true);assert.throws(()=>K.bindUser('../alice'),/登录/);
});
test('changing provider origin requires a freshly entered key',()=>{
 K.bindUser('test');K.save({enabled:true,embedding:{...K.defaults.embedding,api_key:'same-origin-secret'}});
 const values={enabled:'on',embedding_url:'https://other.example.com/v1/embeddings',embedding_model:'new-model',rerank_url:K.defaults.rerank.url,rerank_model:K.defaults.rerank.model};
 const data={get:k=>values[k]||''};assert.equal(K.fromForm(data).embedding.api_key,'');values.key='new-explicit-key';assert.equal(K.fromForm(data).embedding.api_key,'new-explicit-key');assert.equal(K.fromForm(data).rerank.api_key,'');
 values.key='';values.rerank_url='https://other.example.com/v1/rerank';values.rerankEnabled='on';values.enabled='';
 assert.equal(K.fromForm(data).rerank.api_key,'');assert.throws(()=>K.save(K.fromForm(data)),/有效/);
});
test('retrieval output escapes source text and describes fallback without suitability percentages',()=>{
 const esc=v=>String(v??'').replaceAll('<','&lt;').replaceAll('>','&gt;');
 const html=K.resultMarkup({hits:[{title:'<script>',text:'<img onerror=x>',document_id:'doc',index:0,keyword:1,cosine:.82,rerank_score:.9}],retrieval:{mode:'hybrid+rerank',milliseconds:12,warnings:['RETRIEVAL_INDEX_PARTIAL'],candidates:20}},esc);
 assert.equal(html.includes('<script>'),false);assert.equal(html.includes('<img'),false);assert.match(html,/部分材料/);assert.match(html,/不代表岗位匹配度/);assert.equal(html.includes('82%'),false);
});
