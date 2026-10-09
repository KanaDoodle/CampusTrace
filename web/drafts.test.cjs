const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
function setup(){const stored=new Map(),c={Date,localStorage:{getItem:k=>stored.get(k)||null,setItem:(k,v)=>stored.set(k,v),removeItem:k=>stored.delete(k)}};vm.createContext(c);vm.runInContext(fs.readFileSync(__dirname+'/drafts.js','utf8'),c);return {D:c.CampusDrafts,stored};}
const field=(name,value)=>({name,type:'textarea',value,checked:false});const rows=[{id:'add-project',baseline:[field('description','')],value:[field('description','实现幂等消费')]}];
test('persistent edit drafts require opt in, isolate accounts/scopes and expire after seven days',()=>{
 const {D,stored}=setup();assert.equal(D.write('alice','projects',rows),false);assert.equal(stored.size,0);stored.set('campustrace:local-drafts-enabled:v1:alice','yes');assert.equal(D.write('alice','projects',rows),true);assert.equal(D.load('alice','projects').length,1);assert.equal(D.load('bob','projects').length,0);assert.equal(D.load('alice','review-x').length,0);
 const k='campustrace:local-drafts:v1:alice:projects';stored.set(k,JSON.stringify({updated:Date.now()-8*86400000,records:rows}));assert.equal(D.load('alice','projects').length,0);assert.equal(stored.has(k),false);D.write('alice','projects',[]);assert.equal(stored.has(k),false);
});
test('credentials, raw resume fields and overlarge records cannot be persisted even through the writer',()=>{
 const {D,stored}=setup();stored.set('campustrace:local-drafts-enabled:v1:alice','yes');for(const name of ['password','api_key','resume_text','consent','token'])assert.equal(D.write('alice','projects',[{...rows[0],value:[field(name,'PRIVATE')]}]),false);assert.equal(D.write('alice','projects',[{...rows[0],value:[field('description','a'.repeat(60001))]}]),false);assert.equal([...stored.values()].some(v=>v.includes('PRIVATE')),false);
});
