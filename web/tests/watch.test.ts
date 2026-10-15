import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseWatchHistory,watchAPI} from '../src/watch-api.ts';
const empty={entries:[],current:[],link:null,online:false,error:'',pending:0};
test('watch reads distinguish complete empty history from missing or malformed state',()=>{
 assert.deepEqual(parseWatchHistory(empty),empty);
 for(const raw of [{}, {...empty,current:undefined}, {...empty,entries:[{id:1,media_id:'a',title:'A',kind:'baseline',observed_at:new Date().toISOString(),state:{played:false}}]}])assert.throws(()=>parseWatchHistory(raw));
});
test('watch edits carry reviewed revisions and accept only explicit pending acknowledgment',async t=>{
 let calls=0;
 t.mock.method(globalThis,'fetch',async (_url: string | URL | Request,init?: RequestInit)=>{calls++;assert.deepEqual(JSON.parse(init?.body as string),{revision:7,action:'reset'});return Response.json({status:'accepted',id:'write'});});
 await watchAPI.edit('media',7,'reset');assert.equal(calls,1);
});
test('ambiguous watch write responses are surfaced without retrying',async t=>{
 let calls=0;t.mock.method(globalThis,'fetch',async()=>{calls++;return Response.json({status:'saved'});});
 await assert.rejects(watchAPI.edit('media',1,'watched'));assert.equal(calls,1);
});
