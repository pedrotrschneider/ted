import test from 'node:test';import assert from 'node:assert/strict';
import {draftMappingStatus,mergeSourceAssignments,sourceAssignments,draftBatches,seasonFileIDs} from '../src/episode-draft.ts';
import {applySeasonRange,matchPattern} from '../src/episode-matching.ts';
import {saveEpisodeDraft} from '../src/save-episode-draft.ts';
import type {TorrentDetail} from '../src/rd-api.ts';
const source=(id:string):TorrentDetail=>({id,hash:(id==='A'?'a':'b').repeat(40),name:`Torrent ${id}`,bytes:300,status:'downloaded',progress:100,added:'2026-10-08T00:00:00Z',files:[1,2,3].map(id=>({id,path:`/E${id}.mkv`,bytes:100,selected:true,video:true,suggested_exclusion:false}))});
const ep=(season:number,number:number)=>({id:`tt1245698:${season}:${number}`,season,number,name:'Episode'});
const one=ep(1,1),two=ep(1,2),three=ep(2,1),a=source('A'),b=source('B');
test('switching sources and rebuilding the editor projection preserves drafts with colliding file IDs',()=>{
 let draft=mergeSourceAssignments({},a,{[one.id]:1});draft=mergeSourceAssignments(draft,b,{[two.id]:1});
 assert.deepEqual(sourceAssignments(draft,a),{[one.id]:1});assert.deepEqual(sourceAssignments(draft,b),{[two.id]:1});
 // Reopening a modal uses the same parent-owned draft, without rewriting either snapshot.
 draft=mergeSourceAssignments(draft,a,{...sourceAssignments(draft,a),[three.id]:2});
 assert.equal(draft[two.id].source.name,'Torrent B');assert.equal(draft[one.id].file.path,'/E1.mkv');
 assert.equal(draftBatches(draft).length,2);
 const matched=matchPattern(b.files,[one,two,three],'E{%e}',sourceAssignments(draft,b),new Set(Object.keys(draft)));
 assert.equal(matched.added,0);assert.equal(draft[one.id].source.id,'A');
});
test('immediate season ranges replace only their season and highlights are restored from assignments',()=>{
 let draft=mergeSourceAssignments({},b,{[three.id]:1});
 draft=mergeSourceAssignments(draft,a,applySeasonRange(a.files,[one,two],1,sourceAssignments(draft,a),new Set()));
 assert.deepEqual(seasonFileIDs(draft,a,[one.id,two.id],[]),[1,2]);
 assert.deepEqual(seasonFileIDs(draft,b,[three.id],[]),[1]);
 assert.deepEqual(seasonFileIDs(draft,a,[three.id],[]),[]);
 draft=mergeSourceAssignments(draft,a,applySeasonRange(a.files,[one,two],2,sourceAssignments(draft,a),new Set()));
 assert.deepEqual(seasonFileIDs(draft,a,[one.id,two.id],[]),[2,3]);assert.equal(draft[three.id].source.id,'B');
 assert.throws(()=>applySeasonRange(a.files,[one,two],3,sourceAssignments(draft,a),new Set()),/only 1 remain/);
 assert.deepEqual(seasonFileIDs(draft,a,[one.id,two.id],[]),[2,3]);
});
test('source refresh preserves reviewed file identity and failed validation cannot mutate drafts',()=>{
 const draft=mergeSourceAssignments({},a,{[one.id]:1}),changed={...a,files:a.files.map(f=>({...f,bytes:999}))};
 const updated=mergeSourceAssignments(draft,changed,{[one.id]:1,[two.id]:2});
 assert.equal(updated[one.id].file.bytes,100);assert.equal(updated[two.id].file.bytes,999);
 assert.throws(()=>mergeSourceAssignments(draft,a,{[one.id]:99}));assert.equal(draft[one.id].file.bytes,100);
});
test('multi-source submission is one request and retains the complete draft until confirmed',async()=>{
 const original=globalThis.fetch;const draft=mergeSourceAssignments(mergeSourceAssignments({},a,{[one.id]:1}),b,{[two.id]:1});let calls=0;
 try{
 globalThis.fetch=async(_url,init)=>{calls++;const body=JSON.parse(String(init?.body));assert.deepEqual(body.files.map((f:{torrent_id:string})=>f.torrent_id),['A','B']);return Response.json({publication_mode:'native',id:'operation',phase:'queued',error:'',total:2,validated:0,batch:1,batches:1,completed_batches:0,committed:0,retryable:false})};
 const result=await saveEpisodeDraft('tt1245698','anime',draft);assert.equal(result.phase,'queued');assert.equal(calls,1);assert.deepEqual(Object.keys(draft),[one.id,two.id]);
 globalThis.fetch=async()=>Response.json({error:'failed'},{status:503});await assert.rejects(saveEpisodeDraft('tt1245698','anime',draft));assert.deepEqual(Object.keys(draft),[one.id,two.id]);
 }finally{globalThis.fetch=original}
});

test('draft review distinguishes saved, newly assigned and unmapped episodes',()=>{
 const saved={episode_id:one.id,media_id:'media',source_name:a.name,torrent_id:a.id,hash:a.hash,file_id:1,path:'/E1.mkv',bytes:100};
 assert.equal(draftMappingStatus(undefined,saved),'unchanged');
 assert.equal(draftMappingStatus(undefined,undefined),'unmapped');
 assert.equal(draftMappingStatus({source:a,file:a.files[0]},undefined),'modified');
 assert.equal(draftMappingStatus({source:a,file:a.files[0]},saved),'unchanged');
});
test('review detects changed source or file identity even when file IDs collide',()=>{
 const saved={episode_id:one.id,media_id:'media',source_name:a.name,torrent_id:a.id,hash:a.hash,file_id:1,path:'/E1.mkv',bytes:100};
 assert.equal(draftMappingStatus({source:b,file:b.files[0]},saved),'modified');
 assert.equal(draftMappingStatus({source:a,file:{...a.files[0],bytes:101}},saved),'modified');
 assert.equal(draftMappingStatus({source:a,file:a.files[1]},saved),'modified');
});
