import test from 'node:test';
import assert from 'node:assert/strict';
import { streamingApi, prepareSeries } from '../src/streaming-api.ts';

test('preparation status distinguishes verified publication from malformed status', async () => {
 const original=globalThis.fetch;
 try {
  for(const phase of ['draft','probing','ready']) {globalThis.fetch=async()=>Response.json({publication_mode:'native',mapped:true,validated:false,adding:false,phase,published:phase==='ready',error:'',job_id:phase==='draft'?null:1});assert.equal((await streamingApi.status('tt1345836')).phase,phase);}
  globalThis.fetch=async()=>Response.json({publication_mode:'native',mapped:true,validated:false,adding:false,phase:'probing',published:true,error:'',job_id:1});await assert.rejects(streamingApi.status('tt1345836'),/Invalid preparation status/);
  globalThis.fetch=async()=>new Response('',{status:503});await assert.rejects(streamingApi.status('tt1345836'));
 } finally {globalThis.fetch=original;}
});
test('preparation and cancellation require an explicit acknowledgment and are never retried',async()=>{
 const original=globalThis.fetch;let calls=0;
 try{
  globalThis.fetch=async(_,init)=>{calls++;assert.equal(init?.method,'DELETE');return Response.json({status:'cancelled'})};await streamingApi.change('tt1345836',true);assert.equal(calls,1);
  globalThis.fetch=async()=>{calls++;return Response.json({})};await assert.rejects(streamingApi.change('tt1345836',false),/not confirmed/);assert.equal(calls,2);
 }finally{globalThis.fetch=original;}
});

test('group preparation distinguishes whole series, specials and explicit mapped-only scope',async()=>{
 const original=globalThis.fetch;try{
 let body:unknown;
 globalThis.fetch=async(url,init)=>{assert.equal(url,'/api/admin/series/tt1245698/streaming');body=JSON.parse(String(init?.body));return Response.json({total:3,queued:1,existing:1,missing:1})};
 await prepareSeries('tt1245698',0,true);assert.deepEqual(body,{season:0,mapped_only:true});
 await prepareSeries('tt1245698',undefined,false);assert.deepEqual(body,{mapped_only:false});
 globalThis.fetch=async()=>Response.json({total:3,queued:1,existing:0,missing:0});await assert.rejects(prepareSeries('tt1245698',1,false));
 }finally{globalThis.fetch=original}
});
