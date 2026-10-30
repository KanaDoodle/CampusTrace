import {test} from 'node:test';
import assert from 'node:assert/strict';
import {validSearch,validFixture} from './contract.mjs';
const user='a'.repeat(32), other='b'.repeat(32), id='c'.repeat(32);
const response=()=>({mode:'fulltext',more:false,job_ids:[id],jobs:[{id,company:'Synthetic',visibility:'PRIVATE',owner_id:user}]});
test('a normal response and an explicit fallback preserve the same authorization contract',()=>{
 const body=response();assert.equal(validSearch(body,user,'Synthetic',10),true);body.mode='basic';assert.equal(validSearch(body,user,'Synthetic',10),true);
});
test('another account, inconsistent metadata and duplicate hits fail the load check',()=>{
 let body=response();body.jobs[0].owner_id=other;assert.equal(validSearch(body,user,'Synthetic',10),false);
 body=response();body.jobs[0].id=other;assert.equal(validSearch(body,user,'Synthetic',10),false);
 body=response();body.jobs.push(body.jobs[0]);body.job_ids.push(id);assert.equal(validSearch(body,user,'Synthetic',10),false);
 assert.equal(validSearch({mode:'fulltext',more:false,jobs:[],job_ids:[]},user,'Synthetic',10),false);
});
test('the load script refuses a target outside its owned synthetic lab',()=>{
 const run='1'.repeat(12),fixture={schema_version:1,run_id:run,base_url:'http://api:8080',users:[{id:user,token:'t'.repeat(100),topics:['loadterm1']},{id:other,token:'t'.repeat(100),topics:['loadterm0']}]};
 assert.equal(validFixture(fixture,run),true);assert.equal(validFixture({...fixture,base_url:'https://example.com'},run),false);assert.equal(validFixture(fixture,'2'.repeat(12)),false);
});
