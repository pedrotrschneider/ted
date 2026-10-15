import {test} from 'node:test';
import assert from 'node:assert/strict';
import {rdApi,appendPage,visibleTorrents} from '../src/rd-api.ts';
import {ApiError} from '../src/api.ts';
const torrent={id:'T1',name:'Movie',bytes:100,status:'downloaded',progress:100,added:'2026-10-08T12:00:00Z'};
test('RD client validates complete pages and refuses missing fields',async t=>{
 const mock=t.mock.method(globalThis,'fetch',async()=>Response.json({page:1,total:1,torrents:[torrent]}));
 assert.equal((await rdApi.list()).torrents[0].id,'T1');
 mock.mock.mockImplementation(async()=>Response.json({page:1,total:1,torrents:[{id:'T1'}]}));
 await assert.rejects(rdApi.list(),ApiError);
});
test('overlapping or changed pages preserve the previous complete page',()=>{
 const first={page:1,total:2,next_page:2,torrents:[torrent]};
 const last={page:2,total:2,torrents:[{...torrent,id:'T2'}]};
 assert.equal(appendPage(first,last).torrents.length,2);
 assert.throws(()=>appendPage(first,{...last,total:3}),/list changed/);
 assert.throws(()=>appendPage(first,{...last,torrents:[torrent]}),/list changed/);
 assert.throws(()=>appendPage(first,{...last,torrents:[]}),/incomplete/);
 assert.equal(first.torrents.length,1);
});
test('search and deterministic sorting do not mutate provider order',()=>{
 const second={...torrent,id:'T2',name:'Birdy',bytes:200};const rows=[torrent,second];
 assert.deepEqual(visibleTorrents(rows,'bird','name'),[second]);
 assert.deepEqual(visibleTorrents(rows,'','size'),[second,torrent]);
 assert.equal(rows[0],torrent);
});
test('RD credential and rate-limit failures are distinct from TED login',async t=>{
 const mock=t.mock.method(globalThis,'fetch',async()=>Response.json({code:'rd_credentials',error:'arbitrary sensitive text'},{status:502}));
 await assert.rejects(rdApi.list(),(error:unknown)=>error instanceof ApiError&&error.status===502&&!error.message.includes('arbitrary'));
 mock.mock.mockImplementation(async()=>Response.json({code:'rd_limited'},{status:429}));
 await assert.rejects(rdApi.list(),/Real-Debrid is rate limiting/);
});
test('file inspection preserves sample and extra flags without changing RD selection',async t=>{
 const files=[{id:9,path:'/movie.mkv',bytes:100,selected:true,video:true,suggested_exclusion:false},{id:21,path:'/Extra/trailer.mkv',bytes:10,selected:true,video:true,suggested_exclusion:true}];
 t.mock.method(globalThis,'fetch',async(_path:string,init:RequestInit)=>{assert.equal(init.method,'GET');return Response.json({...torrent,hash:'0'.repeat(40),files});});
 const detail=await rdApi.detail('T1');assert.equal(detail.files[1].selected,true);assert.equal(detail.files.filter(file=>file.video&&!file.suggested_exclusion).length,1);
});
