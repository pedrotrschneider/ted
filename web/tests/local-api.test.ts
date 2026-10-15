import test from 'node:test';
import assert from 'node:assert/strict';
import { localApi } from '../src/local-api.ts';

test('local progress validates counts and never treats failure as an absent copy',async()=>{
 const original=globalThis.fetch;
 try {
  const state={speed_mbps:null,remaining_seconds:null,phase:'validating',completed_bytes:1000,validated_bytes:500,total_bytes:1000,error:'',configured:true,eligible:true,mapped:true};
  globalThis.fetch=async()=>Response.json(state);assert.equal((await localApi.status('tt1345836')).validated_bytes,500);
  globalThis.fetch=async()=>Response.json({...state,validated_bytes:1001});await assert.rejects(localApi.status('tt1345836'),/Invalid local/);
  globalThis.fetch=async()=>Response.json({...state,speed_mbps:-1});await assert.rejects(localApi.status('tt1345836'),/Invalid local/);
  globalThis.fetch=async()=>Response.json({...state,speed_mbps:100,remaining_seconds:120});assert.equal((await localApi.status('tt1345836')).speed_mbps,100);
  globalThis.fetch=async()=>Response.json({...state,mapped:undefined});await assert.rejects(localApi.status('tt1345836'),/Invalid local/);
  globalThis.fetch=async()=>Response.json({...state,mapped:false});assert.equal((await localApi.status('tt1345836')).mapped,false);
  globalThis.fetch=async()=>new Response('',{status:503});await assert.rejects(localApi.status('tt1345836'));
 }finally{globalThis.fetch=original}
});
test('download and destructive actions use distinct authorized endpoints and require acknowledgment',async()=>{
 const original=globalThis.fetch;
 try {
  for(const [action,path,method,status] of [['download','/api/movies/tt1345836/local','POST','queued'],['cancel','/api/admin/movies/tt1345836/local/cancel','POST','cancelled'],['remove','/api/admin/movies/tt1345836/local','DELETE','removing']] as const){
   globalThis.fetch=async(url,init)=>{assert.equal(url,path);assert.equal(init?.method,method);return Response.json({status})};await localApi.change('tt1345836',action);
  }
  globalThis.fetch=async()=>Response.json({status:'ready'});await assert.rejects(localApi.change('tt1345836','remove'),/not confirmed/);
 }finally{globalThis.fetch=original}
});

test('show and specials download scopes use one request and validate batch acknowledgment',async()=>{
 const {downloadSeries}=await import('../src/local-api.ts');const original=globalThis.fetch;
 try{
  for(const season of [undefined,0,2]){
   globalThis.fetch=async(url,init)=>{assert.equal(url,'/api/series/tt1245698/local');assert.equal(init?.method,'POST');assert.deepEqual(JSON.parse(String(init?.body)),season===undefined?{ready_only:true}:{season,ready_only:true});return Response.json({total:3,queued:1,existing:1,missing:1})};
   assert.equal((await downloadSeries('tt1245698',season,true)).queued,1);
  }
  globalThis.fetch=async()=>Response.json({total:3,queued:1,existing:0,missing:0});
  await assert.rejects(downloadSeries('tt1245698',1,false),/not confirmed/);
 }finally{globalThis.fetch=original}
});
