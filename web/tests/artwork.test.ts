import {test} from 'node:test';
import assert from 'node:assert/strict';
import {artworkApi,artworkPage,artworkChoices} from '../src/artwork-api.ts';
import {titleArtworkURL} from '../src/artwork-url.ts';
import {movie} from '../src/catalog-api.ts';
const id='11111111-1111-4111-8111-111111111111';
const entity='22222222-2222-4222-8222-222222222222';
const image={id,entity_id:entity,name:'Movie',kind:'movie' as const,provider:'Tmdb' as const,type:'Primary',index:0,status:'ready' as const,tag:'a'.repeat(64),url:`/api/metadata/images/${id}`,generation:3,user_modified:true,has_source:false};
const choice={id,type:'Primary',url:'https://image.tmdb.org/t/p/original/1.png',preview:'https://image.tmdb.org/t/p/w300/1.png',language:'en',width:100,height:200};
test('artwork transport distinguishes cache preparation and retains complete cached images',()=>{
 const cold={images:[],metadata_pending:true,metadata_cached:false,metadata_status:'queued',metadata_error:''};
 assert.deepEqual(artworkPage(cold),cold);
 const ready={...cold,images:[image],metadata_pending:false,metadata_cached:true,metadata_status:'ready'};
 assert.deepEqual(artworkPage(ready),ready);
 assert.throws(()=>artworkPage({...ready,images:[image,image]}));
 assert.throws(()=>artworkPage({...ready,images:[{...image,url:'https://foreign.invalid/image'}]}));
});
test('provider candidates and browser poster routes reject unsafe URLs and malformed lists',()=>{
 const data={status:'ready',generation:1,error_code:'',images:[choice]};assert.deepEqual(artworkChoices(data),data);
 for(const url of ['http://image.tmdb.org/a','https://image.tmdb.org@evil.invalid/a','https://image.tmdb.org:8443/a','javascript:alert(1)'])assert.throws(()=>artworkChoices({...data,images:[{...choice,url}]}));
 const url=`${image.url}?v=${image.tag}`;assert.equal(titleArtworkURL(url),true);
 for(const bad of [image.url,`${url}&secret=x`,'//foreign.invalid/image','/api/admin/providers',`https://ted.example${url}`])assert.equal(titleArtworkURL(bad),false);
 assert.equal(movie({id:'tt1234567',type:'movie',name:'Movie',year:'2026',description:'',poster:url,genres:[]}).poster,url);
});
test('editor accepts one active title slot per supported type and season posters only',()=>{
 const page={images:[image],metadata_pending:false,metadata_cached:true,metadata_status:'ready',metadata_error:''};
 for(const type of ['Primary','Logo','Banner','Backdrop'])assert.equal(artworkPage({...page,images:[{...image,type}]}).images[0].type,type);
 assert.equal(artworkPage({...page,images:[{...image,kind:'season'}]}).images[0].kind,'season');
 for(const change of [{index:1},{kind:'episode'},{type:'Art'},{type:'Thumb'},{type:'Screenshot'},{kind:'season',type:'Backdrop'}])assert.throws(()=>artworkPage({...page,images:[{...image,...change}]}));
 const another='33333333-3333-4333-8333-333333333333';
 assert.throws(()=>artworkPage({...page,images:[image,{...image,id:another,url:`/api/metadata/images/${another}`}]}));
});
test('uploads preserve binary bytes and choices send only scoped IDs and generations',async()=>{
 const previous=globalThis.fetch;
 try{
  const file=new File([new Uint8Array([1,2,3])],'anything.png',{type:'image/png'});
  globalThis.fetch=async(path,options)=>{assert.equal(path,`/api/admin/artwork/${id}/upload?generation=3`);assert.equal(options?.body,file);assert.equal((options?.headers as Record<string,string>)['Content-Type'],'image/png');assert.equal(options?.credentials,'same-origin');return new Response('{"status":"saved"}')};
  assert.equal(await artworkApi.upload(image,file),'saved');
  globalThis.fetch=async(path,options)=>{assert.equal(path,`/api/admin/artwork/${id}/select`);assert.deepEqual(JSON.parse(String(options?.body)),{generation:3,choice_id:id});return new Response('{"status":"queued"}')};
  assert.equal(await artworkApi.select(image,choice),'queued');
  globalThis.fetch=async()=>new Response('{}');await assert.rejects(()=>artworkApi.select(image,choice),/did not confirm/);
 }finally{globalThis.fetch=previous}
});
