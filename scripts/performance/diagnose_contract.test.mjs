import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validDiagnosis } from './diagnose_contract.mjs';
const job = { id:'a', owner_id:'owner', company:'company', visibility:'PRIVATE' };
const body = (jobs, mode='basic') => ({ jobs, job_ids:jobs.map(j=>j.id), mode, more:false });
const valid = (b, kind, id='a') => validDiagnosis(b,'owner','company','basic',kind,id);
test('empty response is valid for miss, fails common/sparse', () => {
  assert.equal(valid(body([]),'miss'),true);
  assert.equal(valid(body([]),'sparse'),false);
  assert.equal(valid(body([]),'common'),false);
});
test('sparse requires exact identity and authorized mode/owner', () => {
  assert.equal(valid(body([job]),'sparse'),true);
  assert.equal(valid(body([job]),'sparse','other'),false);
  assert.equal(valid(body([{...job,owner_id:'other'}]),'sparse'),false);
  assert.equal(valid(body([job],'fulltext'),'sparse'),false);
  assert.equal(valid(body([job]),'miss'),false);
});
test('common response requires ten distinct consistent IDs', () => {
  const b=body(Array.from({length:10},(_,i)=>({...job,id:String(i)})));
  assert.equal(valid(b,'common'),true);
  b.job_ids[1]=b.job_ids[0];
  assert.equal(valid(b,'common'),false);
});
