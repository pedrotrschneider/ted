import test from 'node:test';
import assert from 'node:assert/strict';
import {episodeDownload,readinessBadge} from '../src/episode-status.ts';
import type {LocalState} from '../src/local-api.ts';
const local:LocalState={phase:'downloading',completed_bytes:400,validated_bytes:0,total_bytes:1000,speed_mbps:100,remaining_seconds:1,error:'',configured:true,eligible:true,mapped:true};
test('active transfer can pause or delete; paused prefix can resume or delete',()=>{
 assert.equal(episodeDownload(local).percent,40);
 assert.equal(episodeDownload(local).canPause,true);
 assert.equal(episodeDownload(local).canRemove,true);
 const paused=episodeDownload({...local,phase:'cancelled'});
 assert.equal(paused.canPause,false);assert.equal(paused.canDownload,true);assert.equal(paused.percent,40);
 assert.equal(episodeDownload({...local,phase:'ready'}).showProgress,false);
 assert.equal(episodeDownload({...local,phase:'removing'}).canRemove,false);
});
test('verification uses verified bytes and unknown totals stay indeterminate',()=>{
 assert.equal(episodeDownload({...local,phase:'validating',validated_bytes:100}).percent,10);
 assert.equal(episodeDownload({...local,total_bytes:0,completed_bytes:0}).percent,null);
});
test('pending replacements show adding while existing publication survives; unavailable is unknown',()=>{
 const ready={publication_mode:'native' as const,phase:'ready' as const,published:true,mapped:true,validated:false,adding:false,error:'',job_id:null};
 assert.equal(readinessBadge(ready).tone,'success');
 assert.equal(readinessBadge({...ready,adding:true}).tone,'pending');
 assert.equal(readinessBadge({publication_mode:'native' as const,phase:'draft',published:false,mapped:true,validated:false,adding:false,error:'',job_id:null}).tone,'error');
 assert.equal(readinessBadge(null).tone,'unknown');
});

test('validated episodes wait for atomic batch activation',()=>{
 const pending={publication_mode:'native' as const,phase:'draft' as const,published:false,mapped:true,adding:true,validated:true,error:'',job_id:null};
 assert.equal(readinessBadge(pending).tone,'pending');
 assert.equal(readinessBadge({...pending,adding:false,phase:'ready',published:true}).tone,'success');
});
