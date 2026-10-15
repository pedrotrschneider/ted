import {test} from 'node:test';
import assert from 'node:assert/strict';
import {providerApi} from '../src/provider-api.ts';
import {ApiError} from '../src/api.ts';
const flags={revision:2,rd_configured:true,tmdb_configured:false,tvdb_configured:true,tvdb_pin_configured:false};
test('provider credential changes preserve omitted keys and never return secret fields',async()=>{
 const previous=globalThis.fetch;
 try{
  globalThis.fetch=async(path,options)=>{assert.equal(path,'/api/admin/providers');assert.equal(options?.method,'PUT');assert.deepEqual(JSON.parse(String(options?.body)),{revision:1,rd_key:''});return new Response(JSON.stringify({...flags,ignored_secret:'private'}),{status:200})};
  assert.deepEqual(await providerApi.save({revision:1,rd_key:''}),flags);
 }finally{globalThis.fetch=previous}
});
test('invalid provider status and stale saves stay reviewable errors',async()=>{
 const previous=globalThis.fetch;
 try{
  globalThis.fetch=async()=>new Response(JSON.stringify({...flags,rd_configured:'private-key'}),{status:200});
  await assert.rejects(()=>providerApi.status(),e=>e instanceof ApiError&&!e.message.includes('private-key'));
  globalThis.fetch=async()=>new Response(JSON.stringify({code:'settings_changed'}),{status:409});
  await assert.rejects(()=>providerApi.save({revision:1,rd_key:'new'}),e=>e instanceof ApiError&&e.status===409&&e.message.includes('Reload'));
 }finally{globalThis.fetch=previous}
});
test('immediate removal clears only the selected saved credential using the current revision',async()=>{
 const previous=globalThis.fetch;
 try{
  for(const key of ['rd_key','tmdb_token','tvdb_key','tvdb_pin'] as const){
   let calls=0;
   globalThis.fetch=async(path,options)=>{calls++;assert.equal(path,'/api/admin/providers');assert.equal(options?.method,'PUT');assert.deepEqual(JSON.parse(String(options?.body)),{revision:2,[key]:''});return new Response(JSON.stringify(flags),{status:200})};
   assert.deepEqual(await providerApi.save({revision:2,[key]:''}),flags);
   assert.equal(calls,1);
  }
 }finally{globalThis.fetch=previous}
});
