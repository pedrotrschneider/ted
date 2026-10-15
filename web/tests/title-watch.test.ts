import test from 'node:test';
import assert from 'node:assert/strict';
import {watchScope,titleWatchAPI} from '../src/title-watch-api.ts';
const item=(id:string,played=false)=>({id,played,available:true,writable:true,pending:false,error:'',revision:1});
test('native movie and episode states without retired sequence fields remain editable',async()=>{
 const original=globalThis.fetch;
 try{
  for(const id of ['tt1234567','tt1234567:1:1']){
   const value={version:'current',items:[item(id)]};
   globalThis.fetch=async()=>Response.json(value);
   const result=await titleWatchAPI.read('tt1234567');
   assert.equal(watchScope(result).available,true);
   globalThis.fetch=async(_url,init)=>{assert.equal(JSON.parse(String(init?.body)).version,'current');return Response.json({status:'accepted'})};
   await titleWatchAPI.set('tt1234567',result.version,true,id.includes(':')?id:undefined);
  }
  for(const invalid of [{...item('tt1234567'),revision:-1},{...item('tt1234567'),writable:null}]){
   globalThis.fetch=async()=>Response.json({version:'v',items:[invalid]});
   await assert.rejects(titleWatchAPI.read('tt1234567'));
  }
 }finally{globalThis.fetch=original}
});
test('season and show completion use fully watched available episodes, including specials',()=>{
 const value={version:'v',items:[item('tt1234567:0:1'),item('tt1234567:1:1',true),item('tt1234567:1:2',true),{...item('tt1234567:2:1'),available:false}]};
 assert.equal(watchScope(value).played,false);
 assert.equal(watchScope(value,undefined,1).played,true);
 assert.equal(watchScope(value,undefined,0).played,false);
 assert.equal(watchScope(value,'tt1234567:1:1').played,true);
 assert.equal(watchScope(value,'tt1234567:2:1').available,true);
 value.items[0].played=true;assert.equal(watchScope(value).played,false);value.items[3].played=true;assert.equal(watchScope(value).played,true);
});
test('pending confirmation disables parent scopes; missing observation cannot silently shrink a batch',()=>{
 const value={version:'v',items:[{...item('tt1234567:1:1'),pending:true},item('tt1234567:2:1')]};
 assert.equal(watchScope(value).pending,true);
 assert.equal(watchScope(value,undefined,2).pending,false);
 value.items[1].writable=false;assert.equal(watchScope(value).available,false);
 assert.equal(watchScope(undefined).available,false);
});
test('writes carry reviewed version and precise scope, malformed reads and ambiguous writes fail',async()=>{
 const original=globalThis.fetch;
 try{
 globalThis.fetch=async(url,init)=>{assert.equal(url,'/api/watch-titles/tt1234567');assert.deepEqual(JSON.parse(String(init?.body)),{version:'v',played:true,season:0});return Response.json({status:'accepted'})};
 await titleWatchAPI.set('tt1234567','v',true,undefined,0);
 globalThis.fetch=async()=>Response.json({items:[{id:'x'}],version:'v'});await assert.rejects(titleWatchAPI.read('tt1234567'));
 globalThis.fetch=async()=>Response.json({status:'unknown'});await assert.rejects(titleWatchAPI.set('tt1234567','v',false));
 }finally{globalThis.fetch=original}
});
test('dated and undated choices are sent explicitly for title and episode scopes',async()=>{
 const original=globalThis.fetch;
 try{
  for(const mode of ['now','undated'] as const){
   globalThis.fetch=async(_url,init)=>{assert.deepEqual(JSON.parse(String(init?.body)),{version:'v',played:true,episode:'tt1234567:1:1',date_mode:mode});return Response.json({status:'accepted'})};
   await titleWatchAPI.set('tt1234567','v',true,'tt1234567:1:1',undefined,mode);
  }
 }finally{globalThis.fetch=original}
});

test('forget history explicitly requests a whole-title unwatch',async()=>{
 const previous=globalThis.fetch;
 try{globalThis.fetch=async(path,options)=>{assert.equal(path,'/api/watch-titles/tt1234567');assert.deepEqual(JSON.parse(String(options?.body)),{version:'v',played:false,forget_history:true});return Response.json({status:'accepted'})};await titleWatchAPI.set('tt1234567','v',false,undefined,undefined,undefined,true)}finally{globalThis.fetch=previous}
});
