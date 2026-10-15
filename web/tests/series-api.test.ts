import {saveEpisodeDraft} from '../src/save-episode-draft.ts';
import test from 'node:test';import assert from 'node:assert/strict';
import {seriesApi,appendSeries} from '../src/series-api.ts';
import {streamingApi} from '../src/streaming-api.ts';
import {localApi} from '../src/local-api.ts';
const title={id:'tt1245698',type:'series' as const,name:'Birdy',year:'2008',description:'',poster:'',genres:[]};
test('series reads reject missing episodes, wrong identity and unstable pagination',async()=>{
 const original=globalThis.fetch;try{
 globalThis.fetch=async()=>Response.json(title);await assert.rejects(seriesApi.detail(title.id));
 globalThis.fetch=async()=>Response.json({...title,episodes:[{id:title.id+':2:1',season:1,number:1,name:'Wrong'}]});await assert.rejects(seriesApi.detail(title.id));
 globalThis.fetch=async()=>Response.json({...title,episodes:[]});assert.deepEqual((await seriesApi.detail(title.id)).episodes,[]);
 assert.throws(()=>appendSeries({series:[title],skip:0,next_skip:50},{series:[title],skip:50,next_skip:100}));
 }finally{globalThis.fetch=original}
});
test('episode operations use separate routes and preserve explicit acknowledgments',async()=>{
 const original=globalThis.fetch;try{
 globalThis.fetch=async(url,init)=>{assert.equal(url,'/api/admin/episodes/tt1245698%3A1%3A1/streaming');assert.equal(init?.method,'POST');return Response.json({status:'queued'})};await streamingApi.change('tt1245698:1:1',false);
 globalThis.fetch=async(url)=>{assert.equal(url,'/api/episodes/tt1245698%3A1%3A1/local');return Response.json({status:'queued'})};await localApi.change('tt1245698:1:1','download');
 }finally{globalThis.fetch=original}
});
test('saving shared files keeps one payload row per episode and rejects missing files before writing',async()=>{
 const original=globalThis.fetch;const source={id:'T1',hash:'a'.repeat(40),name:'Show',bytes:100,status:'downloaded',progress:100,added:'2026-10-08T00:00:00Z',files:[{id:7,path:'/combined.mkv',bytes:100,selected:true,video:true,suggested_exclusion:false}]};let calls=0;
 try{globalThis.fetch=async(_url,init)=>{calls++;const body=JSON.parse(String(init?.body));assert.deepEqual(body.files.map((f:{episode_id:string;file_id:number})=>[f.episode_id,f.file_id]),[['tt1245698:1:1',7],['tt1245698:1:2',7]]);return Response.json({publication_mode:'native',id:'operation',phase:'queued',error:'',total:2,validated:0,batch:1,batches:1,completed_batches:0,committed:0,retryable:false})};
 await saveEpisodeDraft(title.id,'anime',{'tt1245698:1:1':{source,file:source.files[0]},'tt1245698:1:2':{source,file:source.files[0]}});
 await assert.rejects(saveEpisodeDraft(title.id,'anime',{'tt1245698:1:1':{source,file:{...source.files[0],id:8}}}));assert.equal(calls,1);
 }finally{globalThis.fetch=original}
});
