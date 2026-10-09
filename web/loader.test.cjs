const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
function setup(){const inserted=[];const c={document:{createElement:tag=>({tag,remove(){this.removed=true;}}),head:{append:el=>inserted.push(el)}}};vm.createContext(c);vm.runInContext(fs.readFileSync(__dirname+'/loader.js','utf8'),c);return {A:c.CampusAssets,inserted};}
test('concurrent lazy route loads share assets; a network failure can be retried',async()=>{
 const h=setup(),one=h.A.ensure('profile'),two=h.A.ensure('profile');assert.equal(h.inserted.length,h.A.routes.profile.length);for(const el of h.inserted)el.onload();await Promise.all([one,two]);await h.A.ensure('profile');assert.equal(h.inserted.length,h.A.routes.profile.length);
 const first=h.A.ensure('zip');h.inserted.at(-1).onerror();await assert.rejects(first,/重试/);assert.equal(h.inserted.at(-1).removed,true);const retry=h.A.ensure('zip');h.inserted.at(-1).onload();await retry;assert.equal(h.inserted.filter(v=>v.src?.includes('jszip')).length,2);
});
test('route manifest refers to real assets, and sign in does not eagerly load optional features',()=>{
 const h=setup();for(const files of Object.values(h.A.routes))for(const file of files)assert.ok(fs.existsSync(__dirname+'/'+file),file);
 const html=fs.readFileSync(__dirname+'/index.html','utf8');assert.equal([...html.matchAll(/<script /g)].length,5);for(const name of ['profile.js','agent_harness.js','jszip.min.js'])assert.equal(html.includes(name),false);
});
