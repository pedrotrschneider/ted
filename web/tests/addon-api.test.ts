import {test} from 'node:test';
import assert from 'node:assert/strict';
import {addonApi} from '../src/addon-api.ts';
import {ApiError} from '../src/api.ts';
const addon={id:'a'.repeat(64),name:'Fixture',enabled:true,revision:0};
test('add-on switches carry revision and omit private collection fields',async()=>{
 const previous=globalThis.fetch;
 try{
  globalThis.fetch=async(path,options)=>{assert.equal(path,'/api/admin/addons');assert.equal(options?.method,'PUT');assert.deepEqual(JSON.parse(String(options?.body)),{id:addon.id,revision:0,enabled:false});return new Response(JSON.stringify({...addon,enabled:false,revision:1,transportUrl:'private-url'}))};
  assert.deepEqual(await addonApi.save(addon,false),{...addon,enabled:false,revision:1});
 }finally{globalThis.fetch=previous}
});
test('invalid and duplicate policies are failures and stale writes require reload',async()=>{
 const previous=globalThis.fetch;
 try{
  globalThis.fetch=async()=>new Response(JSON.stringify({addons:[addon,addon]}));await assert.rejects(()=>addonApi.list(),ApiError);
  globalThis.fetch=async()=>new Response(JSON.stringify({addons:[{...addon,enabled:'private'}]}));await assert.rejects(()=>addonApi.list(),ApiError);
  globalThis.fetch=async()=>new Response(JSON.stringify({code:'settings_changed'}),{status:409});await assert.rejects(()=>addonApi.save(addon,false),e=>e instanceof ApiError&&e.status===409&&e.message.includes('Reload'));
 }finally{globalThis.fetch=previous}
});
