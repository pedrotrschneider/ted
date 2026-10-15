import test from 'node:test';
import assert from 'node:assert/strict';
import {copyActive,copyApi} from '../src/stremio-copy-api.ts';
import {pageFromHash} from '../src/navigation.ts';
const id='e6c6d3ca-2ca5-4a67-8f28-8a259b396e4c';
const copy={id,source_email:'source@example.com',destination_email:'destination@example.com',phase:'retrying' as const,error:'unavailable',error_title:'tt1234567',total:4,resolved:4,finished:2,watched_titles:3,watched_items:10,next_attempt:'2026-10-13T14:00:00Z',skipped:[{id:'kitsu:1444',name:'Skipped show',reason:'identity_review'}]};
test('temporary admin route restores on reload and never opens for a member',()=>{
 assert.equal(pageFromHash('#stremio-sync',true),'stremio-sync');assert.equal(pageFromHash('#stremio-sync',false),'movies');
});
test('copy status retains retry progress and skipped cards, rejecting incomplete responses',async()=>{
 const original=globalThis.fetch;try{
  globalThis.fetch=async()=>Response.json({copy:null});assert.equal(await copyApi.status(),null);
  globalThis.fetch=async()=>Response.json({copy});const v=await copyApi.status();assert.deepEqual(v?.skipped,copy.skipped);assert.equal(copyActive(v),true);assert.equal(copyActive({...copy,phase:'complete'}),false);
  for(const broken of [{...copy,skipped:undefined},{...copy,total:-1},{...copy,id:'bad'},{...copy,phase:'unknown'}]){globalThis.fetch=async()=>Response.json({copy:broken});await assert.rejects(copyApi.status(),/Incomplete/)}
 }finally{globalThis.fetch=original}
});
test('copy submission sends credentials once and retry/cancel require their own acknowledgments',async()=>{
 const original=globalThis.fetch;try{
  let calls=0;const source={email:'source@example.com',password:'private-source'},destination={email:'destination@example.com',password:'private-destination'};
  globalThis.fetch=async(path,options)=>{calls++;assert.equal(path,'/api/admin/stremio-copy');assert.deepEqual(JSON.parse(String(options?.body)),{source,destination});return Response.json({id})};assert.equal(await copyApi.start(source,destination),id);assert.equal(calls,1);
  globalThis.fetch=async(path)=>{assert.equal(path,'/api/admin/stremio-copy/'+id+'/retry');return Response.json({status:'retry'})};await copyApi.control(id,'retry');
  globalThis.fetch=async()=>Response.json({status:'retry'});await assert.rejects(copyApi.control(id,'cancel'),/Incomplete/);
 }finally{globalThis.fetch=original}
});
