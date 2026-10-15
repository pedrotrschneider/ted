import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mappingOperation,mappingProgressText,mappingOperationApi} from '../src/mapping-operation-api.ts';
import {readinessBadge} from '../src/episode-status.ts';
const state={publication_mode:'native' as const,id:'submission',phase:'validating' as const,error:'',total:500,validated:240,batch:2,batches:3,completed_batches:1,committed:200,retryable:false};
test('batch progress distinguishes saved episodes from validation and partial failure',()=>{
 assert.match(mappingProgressText(mappingOperation(state)),/Batch 2 of 3 · 200 of 500 episodes saved/);
 assert.match(mappingProgressText({...state,phase:'failed',retryable:true}),/completed batches remain available/);
 assert.match(mappingProgressText({...state,phase:'complete',batch:3,completed_batches:3,committed:500,validated:500}),/ready in TED/);
 assert.match(readinessBadge({publication_mode:'native',queued:true,adding:true,mapped:true,validated:false,phase:'draft',error:'',published:false,job_id:null}).label,/queued/);
});
test('malformed batch progress is unknown rather than claimed completion',()=>{
 for(const bad of [{batch:4},{committed:241},{completed_batches:4},{batches:-1},{retryable:'yes'},{validated:501}]){
  assert.throws(()=>mappingOperation({...state,...bad}),/outcome is unknown/);
 }
});
test('retry uses the retained submission and does not repeat a lost write',async()=>{
 const original=globalThis.fetch;let calls=0;
 try{
  globalThis.fetch=async(url,init)=>{calls++;assert.equal(url,'/api/admin/series/tt1245698/mapping-operation/retry');assert.equal(init?.method,'POST');assert.deepEqual(JSON.parse(String(init?.body)),{submission_id:'submission'});return Response.json({...state,phase:'queued'})};
  assert.equal((await mappingOperationApi.retry('series','tt1245698','submission')).batch,2);
  assert.equal(calls,1);
  globalThis.fetch=async()=>{calls++;throw new TypeError('connection lost')};
  await assert.rejects(mappingOperationApi.retry('series','tt1245698','submission'));assert.equal(calls,2);
 }finally{globalThis.fetch=original}
});

test('native progress uses TED activation after source validation',()=>{
 const native=mappingOperation({...state,publication_mode:'native'});
 assert.match(mappingProgressText({...native,phase:'complete',committed:500,validated:500}),/ready in TED/);
 assert.match(mappingProgressText({...native,phase:'publishing'}),/Activating validated mappings in TED/);
 assert.match(readinessBadge({publication_mode:'native',adding:false,mapped:true,validated:false,phase:'ready',error:'',published:true,job_id:null}).label,/Ready in TED/);
 assert.throws(()=>mappingOperation({...native,committed:241}),/outcome is unknown/);
});
