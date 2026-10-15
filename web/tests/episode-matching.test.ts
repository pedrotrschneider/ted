import test from 'node:test';import assert from 'node:assert/strict';
import {orderedEpisodes,orderedFiles,assignEpisode,episodePattern,matchPattern,applySeasonRange} from '../src/episode-matching.ts';
const ep=(season:number,number:number)=>({id:`tt1245698:${season}:${number}`,name:'Episode',season,number});
const file=(id:number,path:string,extra=false)=>({id,path,bytes:100,selected:true,video:true,suggested_exclusion:extra});
test('manual ordering leaves specials last and allows reuse, reassignment and skipping without mutating draft',()=>{
 const episodes=[ep(0,1),ep(2,1),ep(1,2),ep(1,1)];assert.deepEqual(orderedEpisodes(episodes),[episodes[3],episodes[2],episodes[1],episodes[0]]);
 const first=assignEpisode({},episodes[3].id,7);const shared=assignEpisode(first,episodes[2].id,7);
 assert.deepEqual(Object.values(shared),[7,7]);const changed=assignEpisode(shared,episodes[3].id,8);
 assert.equal(shared[episodes[3].id],7);assert.deepEqual(assignEpisode(changed,episodes[2].id,null),{[episodes[3].id]:8});
});
test('pattern expands padded numbers and treats punctuation literally',()=>{
 assert.equal(episodePattern('S{%s}xE{%e.3}')(ep(3,23)),'S3xE023');
 assert.equal(episodePattern('[{%s.1}]({%e})')(ep(12,123)),'[12](123)');
 for(const pattern of ['{%e.0}','{%e.101}','{%x}','S{%s}'])assert.throws(()=>episodePattern(pattern));
 const result=matchPattern([file(1,'/show.[3](23).mkv'),file(2,'/show.323.mkv')],[ep(3,23)],'[{%s}]({%e})',{},new Set());assert.equal(result.assignments[ep(3,23).id],1);
});
test('pattern preserves manual and saved mappings, leaving ambiguous and missing episodes untouched',()=>{
 const episodes=[1,2,3,4,5].map(n=>ep(1,n));const draft={[episodes[0].id]:99};
 const files=[file(1,'E1.mkv'),file(2,'E2.mkv'),file(3,'E3.mkv'),file(4,'E3.alt.mkv'),file(5,'E4.mkv')];
 const result=matchPattern(files,episodes,'E{%e}',draft,new Set([episodes[1].id]));
 assert.deepEqual(result,{assignments:{...draft,[episodes[3].id]:5},added:1,ambiguous:1});assert.deepEqual(draft,{[episodes[0].id]:99});
});
test('season range derives its end and each new start moves the whole range',()=>{
 const files=orderedFiles([file(10,'10.mkv'),file(2,'2.mkv'),file(1,'1.mkv'),file(3,'Extra/3.mkv',true)]);
 const episodes=[ep(1,2),ep(1,1)],draft={[ep(2,1).id]:99};
 assert.deepEqual(applySeasonRange(files,episodes,1,draft,new Set()),{...draft,[ep(1,1).id]:1,[ep(1,2).id]:2});
 assert.deepEqual(applySeasonRange(files,episodes,2,draft,new Set()),{...draft,[ep(1,1).id]:2,[ep(1,2).id]:10});
 assert.deepEqual(applySeasonRange(files,[ep(0,1)],10,{},new Set()),{[ep(0,1).id]:10});
});
test('incomplete or missing starts cannot change drafts; saved episodes remain protected',()=>{
 const files=[file(1,'1.mkv'),file(2,'2.mkv')],episodes=[ep(1,1),ep(1,2)],draft={[ep(2,1).id]:99};
 assert.throws(()=>applySeasonRange(files,episodes,2,draft,new Set()),/only 1 remain/);
 assert.throws(()=>applySeasonRange(files,episodes,999,draft,new Set()),/first file/);
 assert.deepEqual(draft,{[ep(2,1).id]:99});
 assert.deepEqual(applySeasonRange(files,episodes,1,draft,new Set([ep(1,1).id])),{...draft,[ep(1,2).id]:2});
});
