import test from 'node:test';
import assert from 'node:assert/strict';
import {otherProvider,providerLabel,seriesProvider,seriesProviderApi} from '../src/series-provider-api.ts';

test('series provider choices are validated and confirmations carry the reviewed provider',async()=>{
 const value={provider:'Tvdb',revision:0,can_change:true,status:'ready',error_code:'',overrides:{artwork:2,metadata:0,version:'a'.repeat(64)}};
 assert.equal(seriesProvider(value).provider,'Tvdb');
 for(const patch of [{provider:'other'},{revision:-1},{can_change:'true'},{error_code:null},{overrides:null},{overrides:{...value.overrides,artwork:-1}},{overrides:{...value.overrides,version:'old'}}])assert.throws(()=>seriesProvider({...value,...patch}));
 assert.equal(providerLabel(otherProvider('Tvdb')),'TMDB');assert.equal(otherProvider('Tmdb'),'Tvdb');
 const old=globalThis.fetch;const calls:string[]=[];
 try{
  globalThis.fetch=async(url,init)=>{calls.push(String(url));assert.equal(init?.credentials,'same-origin');if(init?.method==='POST'){assert.equal(url,'/api/admin/series/tt1234567/metadata-provider');assert.deepEqual(JSON.parse(String(init?.body)),{expected_provider:'Tvdb',provider:'Tmdb',override_action:'remove',overrides_version:value.overrides.version});return Response.json({status:'queued'},{status:202})}return Response.json(value)};
  assert.equal((await seriesProviderApi.get('tt1234567')).can_change,true);
  await seriesProviderApi.change('tt1234567','Tvdb',{action:'remove',version:value.overrides.version});assert.equal(calls.length,2);
 }finally{globalThis.fetch=old}
});
