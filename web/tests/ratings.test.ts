import test from 'node:test';import assert from 'node:assert/strict';
import {ratings} from '../src/ratings-api.ts';
test('IMDb rating batches distinguish unrated from pending and validate responses',async()=>{
 const original=globalThis.fetch,signal=new AbortController().signal;
 try{
  const records=[{id:'tt0289043',rating:7.5,pending:false},{id:'tt1345836',rating:null,pending:false}];
  globalThis.fetch=async()=>Response.json({ratings:records});
  assert.deepEqual(await ratings('movie',records.map(r=>r.id),signal),records);
  for(const value of [11,-1,'7.5']){
   globalThis.fetch=async()=>Response.json({ratings:[{id:'tt0289043',rating:value,pending:false}]});
   await assert.rejects(ratings('movie',['tt0289043'],signal));
  }
  globalThis.fetch=async()=>Response.json({ratings:[]});await assert.rejects(ratings('movie',['tt0289043'],signal));
 }finally{globalThis.fetch=original}
});
