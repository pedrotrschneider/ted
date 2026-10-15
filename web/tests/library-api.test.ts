import test from 'node:test';import assert from 'node:assert/strict';
import {libraryApi} from '../src/library-api.ts';
test('whole-title removal uses admin routes and preserves pending outcomes',async()=>{
 const original=globalThis.fetch;
 try{
  for(const kind of ['movies','series'] as const){
   globalThis.fetch=async(url,init)=>{assert.equal(url,`/api/admin/${kind}/tt1245698/library`);assert.equal(init?.method,'DELETE');return Response.json({publication_mode:'native',id:'removal',phase:'snapshot',error:''})};
   assert.equal((await libraryApi.remove(kind,'tt1245698')).phase,'snapshot');
  }
  globalThis.fetch=async()=>Response.json({publication_mode:'native',id:'removal',phase:'removing',error:'waiting_for_transfer'});
  assert.equal((await libraryApi.status('series','tt1245698')).phase,'removing');
  globalThis.fetch=async()=>Response.json({publication_mode:'native',id:'removal',phase:'unknown',error:''});
  await assert.rejects(libraryApi.status('series','tt1245698'),/unavailable/);
 }finally{globalThis.fetch=original}
});
