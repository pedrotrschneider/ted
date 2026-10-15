import {test} from 'node:test';
import assert from 'node:assert/strict';
import {rdApi} from '../src/rd-api.ts';
import {ApiError} from '../src/api.ts';
const state={configured:true,ready:true,total:120,files_pending:2,phase:'files',checked_at:'2026-10-13T12:00:00Z',full_rebuilt_at:null,error_code:''};
test('RD cache status separates last check, full rebuild and pending file details',async t=>{
 const mock=t.mock.method(globalThis,'fetch',async()=>Response.json(state));
 assert.equal((await rdApi.cache()).files_pending,2);
 mock.mock.mockImplementation(async()=>Response.json({...state,total:-1}));await assert.rejects(rdApi.cache(),ApiError);
 mock.mock.mockImplementation(async()=>Response.json({...state,checked_at:undefined}));await assert.rejects(rdApi.cache(),ApiError);
});
test('manual RD cache refresh selects incremental or full durable work without retrying lost writes',async t=>{
 const calls:boolean[]=[];
 const mock=t.mock.method(globalThis,'fetch',async(_url:string,init:RequestInit)=>{assert.equal(init.method,'POST');calls.push(JSON.parse(init.body as string).full);return Response.json({...state,phase:'queued'},{status:202})});
 await rdApi.refresh();await rdApi.refresh(true);assert.deepEqual(calls,[false,true]);
 let count=0;mock.mock.mockImplementation(async()=>{count++;throw new Error('lost response')});await assert.rejects(rdApi.refresh());assert.equal(count,1);
});
