import test from 'node:test';import assert from 'node:assert/strict';import {stremioApi} from '../src/stremio-api.ts';
test('Stremio status distinguishes disconnected, initializing and malformed sync',async()=>{
 const original=globalThis.fetch;try{
 globalThis.fetch=async()=>Response.json({linked:false,email:'',sync:null});assert.equal((await stremioApi.status()).linked,false);
 globalThis.fetch=async()=>Response.json({linked:true,email:'test@example.com',sync:null});assert.equal((await stremioApi.status()).sync,null);
 globalThis.fetch=async()=>Response.json({linked:true,email:'test@example.com'});await assert.rejects(stremioApi.status(),/Incomplete/);
 }finally{globalThis.fetch=original}
});
test('Stremio link and disconnect require explicit acknowledgments without write retries',async()=>{
 const original=globalThis.fetch;try{let calls=0;globalThis.fetch=async(path,options)=>{calls++;assert.equal(path,'/api/stremio/link');assert.equal(options?.method,'POST');assert.deepEqual(JSON.parse(String(options?.body)),{email:'test@example.com',password:'private'});return Response.json({status:'linked'})};await stremioApi.link('test@example.com','private');assert.equal(calls,1);
 globalThis.fetch=async()=>Response.json({});await assert.rejects(stremioApi.disconnect(),/Incomplete/);
 }finally{globalThis.fetch=original}
});
test('Stremio sync status reports failures and rejects missing progress fields',async()=>{
 const original=globalThis.fetch;try{
  const status={linked:true,email:'test@example.com',preview:null,import:null,sync:{last_success:null,next_poll:'2026-10-09T12:00:00Z',error:'incomplete',pending:false,issues:0,outgoing:[]}};
  globalThis.fetch=async()=>Response.json(status);assert.equal((await stremioApi.status()).sync?.error,'incomplete');
  for(const sync of [{...status.sync,pending:undefined},{...status.sync,issues:null},{...status.sync,outgoing:[{identity:'tt1234567',name:'Fixture',played:'yes',phase:'pending',error:''}]}]){globalThis.fetch=async()=>Response.json({...status,sync});await assert.rejects(stremioApi.status(),/Incomplete/)}
 }finally{globalThis.fetch=original}
});
