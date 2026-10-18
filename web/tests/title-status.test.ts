import test from 'node:test';
import assert from 'node:assert/strict';
import {parseTitleStatuses,showTitleCount,titleStatusLabel,titleStatusProgress,showWatchCount,watchLabel} from '../src/title-status-api.ts';
const pending={publication_mode:'native' as const,watch:{seen:false,watched:0,available:0,progress:null},id:'tt1245698',state:'pending' as const,added:12,total:26,validated:20,phase:'validating' as const};
test('pending series always show confirmed progress, including final confirmation',()=>{
 assert.equal(showTitleCount(pending,'series'),true);
 assert.equal(showTitleCount({...pending,added:26},'series'),true);
 assert.equal(showTitleCount(pending,'movie'),false);
 assert.match(titleStatusLabel(pending),/20 \/ 26 episodes in this submission/);
});
test('successful partial series show coverage; complete series and untouched titles do not',()=>{
 assert.equal(showTitleCount({...pending,state:'added'},'series'),true);
 assert.equal(showTitleCount({...pending,state:'added',added:26},'series'),false);
 assert.equal(showTitleCount({...pending,state:''},'series'),false);
 assert.equal(showTitleCount({...pending,state:'added',total:null},'series'),true);
 assert.match(titleStatusLabel({...pending,state:'failed'}),/failed/);
});
test('incomplete status reads reject rather than erase previous indicators',()=>{
 assert.deepEqual(parseTitleStatuses({statuses:[pending]},[pending.id]),[pending]);
 for(const statuses of [[],[pending,pending],[{...pending,added:-1}],[{...pending,total:NaN}],[{...pending,state:'bogus'}]])assert.throws(()=>parseTitleStatuses({statuses},[pending.id]));
});

test('pending count follows validation then per-item publication, not previous library coverage',()=>{
 assert.equal(titleStatusProgress(pending),20);
 assert.equal(titleStatusProgress({...pending,phase:'publishing'}),12);
 assert.equal(titleStatusProgress({...pending,state:'added',phase:'complete'}),12);
 assert.match(titleStatusLabel({...pending,phase:'publishing'}),/Activating in TED/);
});

test('watched show count uses main-season episodes and hides only when complete',()=>{
 const w={seen:true,watched:3,available:5,progress:null};
 assert.equal(showWatchCount(w,'series'),true);assert.equal(showWatchCount({...w,watched:5},'series'),false);
 assert.equal(showWatchCount({...w,watched:0},'series'),true);assert.equal(showWatchCount({...w,seen:false},'series'),false);
 assert.equal(showWatchCount(w,'movie'),false);assert.match(watchLabel(w,'series'),/3 \/ 5 released main-season episodes fully watched/);
});
test('poster reads reject malformed viewing state instead of showing an unwatched result',()=>{
 for(const watch of [undefined,{seen:true,watched:2,available:1,progress:null},{seen:true,watched:0,available:0,progress:101}])assert.throws(()=>parseTitleStatuses({statuses:[{...pending,watch}]},[pending.id]));
 assert.match(watchLabel({seen:true,watched:0,available:1,progress:25},'movie'),/25%/);
});

test('other-user indicators are optional, validated and preserve personal watch state',()=>{
 const user={account_id:2,name:'Clara',avatar_version:'a'.repeat(64),watch:{seen:true,watched:2,available:3,progress:null}};
 const result=parseTitleStatuses({statuses:[{...pending,user_watches:[user]}]},[pending.id]);
 assert.deepEqual(result[0].watch,pending.watch);
 assert.equal(result[0].user_watches?.[0].name,'Clara');
 for(const user_watches of [[{...user,account_id:0}],[{...user,avatar_version:'https://private.example'}],[{...user,watch:{...user.watch,watched:4}}],[user,user]]){
  assert.throws(()=>parseTitleStatuses({statuses:[{...pending,user_watches}]},[pending.id]));
 }
});
test('other-user labels distinguish episode counts, partial playback and completion',async()=>{
 const {userWatchLabel,userWatchProgress}=await import('../src/title-status-api.ts');
 const user={account_id:2,name:'Clara',watch:{seen:true,watched:2,available:3,progress:null as number|null}};
 assert.equal(userWatchProgress(user,'series'),'2 / 3');assert.match(userWatchLabel(user,'series'),/^Clara: 2 \/ 3/);
 const partial={...user,watch:{...user.watch,watched:0,available:1,progress:35}};
 assert.equal(userWatchProgress(partial,'movie'),'35%');assert.equal(userWatchLabel(partial,'movie'),'Clara: 35% playback progress');
 const complete={...partial,watch:{...partial.watch,watched:1,progress:null}};
 assert.equal(userWatchProgress(complete,'movie'),null);assert.equal(userWatchLabel(complete,'movie'),'Clara: Watched');
});
