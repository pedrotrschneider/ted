import test from 'node:test';import assert from 'node:assert/strict';
import {assignSeasonStep,seasonSteps,skipSeasonStep,skipMappingStep} from '../src/season-step.ts';
import {seasonFileIDs,nextUnmatchedFile} from '../src/episode-draft.ts';
import type {TorrentDetail} from '../src/rd-api.ts';
const episodes=[{id:'special',season:0,number:1,name:'Special'},{id:'s2',season:2,number:1,name:'Two'},{id:'s1a',season:1,number:1,name:'One'},{id:'s1b',season:1,number:2,name:'One B'}];
const source:TorrentDetail={id:'A',name:'Show',hash:'a'.repeat(40),bytes:300,status:'downloaded',progress:100,added:'2026-10-08T00:00:00Z',files:[1,2,3].map(id=>({id,path:`/${id}.mkv`,bytes:100,selected:true,video:true,suggested_exclusion:false}))};
test('range navigation targets the first unmatched file after the applied range',()=>{
 const next=assignSeasonStep({},source,source.files,episodes,0,1,new Set());
 assert.equal(nextUnmatchedFile(source.files,2,source,next.draft,[]),3);
 const saved=[{episode_id:'s2',torrent_id:source.id,hash:source.hash,file_id:3}];
 assert.equal(nextUnmatchedFile(source.files,2,source,next.draft,saved),undefined);
 assert.equal(nextUnmatchedFile(source.files,3,source,{},[]),undefined);
});
test('next unmatched file respects remaps and does not confuse another torrent file ID',()=>{
 const other={...source,id:'B',hash:'b'.repeat(40)};
 const saved=[{episode_id:'s2',torrent_id:source.id,hash:source.hash,file_id:3}];
 const draft={s2:{source:other,file:other.files[2]}};
 assert.equal(nextUnmatchedFile(source.files,2,source,draft,saved),3);
 assert.equal(nextUnmatchedFile(source.files,2,source,{},[{...saved[0],torrent_id:other.id,hash:other.hash}]),3);
});
test('valid ranges advance season by season with specials last and allow revisiting highlights',()=>{
 assert.deepEqual(seasonSteps(episodes),[1,2,0]);
 const first=assignSeasonStep({},source,source.files,episodes,0,1,new Set());assert.equal(first.index,1);
 const second=assignSeasonStep(first.draft,source,source.files,episodes,first.index,3,new Set());assert.equal(second.index,2);
 assert.deepEqual(seasonFileIDs(second.draft,source,['s1a','s1b'],[]),[1,2]);
 const revised=assignSeasonStep(second.draft,source,source.files,episodes,0,2,new Set());assert.equal(revised.index,1);assert.equal(revised.draft.s2.file.id,3);assert.equal(revised.draft.s1a.file.id,2);
 const final=assignSeasonStep(second.draft,source,source.files,episodes,2,1,new Set());assert.equal(final.index,3);assert.throws(()=>assignSeasonStep(final.draft,source,source.files,episodes,3,1,new Set()),/All seasons/);
});
test('an incomplete range does not advance or mutate existing assignments',()=>{
 const first=assignSeasonStep({},source,source.files,episodes,0,1,new Set());
 assert.throws(()=>assignSeasonStep(first.draft,source,source.files,episodes,0,3,new Set()),/only 1 remain/);
 assert.equal(first.index,1);assert.deepEqual(seasonFileIDs(first.draft,source,['s1a','s1b'],[]),[1,2]);
});
test('skip season preserves a prior source while another torrent supplies the next season',()=>{
 const first=assignSeasonStep({},source,source.files,episodes,0,1,new Set());
 const skipped=skipSeasonStep(first.draft,episodes,0);assert.equal(skipped.index,1);assert.strictEqual(skipped.draft,first.draft);
 const other={...source,id:'B',hash:'b'.repeat(40)};
 const second=assignSeasonStep(skipped.draft,other,other.files,episodes,skipped.index,1,new Set());
 assert.equal(second.draft.s1a.source.id,'A');assert.equal(second.draft.s1b.source.id,'A');assert.equal(second.draft.s2.source.id,'B');
 assert.strictEqual(skipSeasonStep(second.draft,episodes,3).draft,second.draft);assert.equal(skipSeasonStep(second.draft,episodes,3).index,3);
});
test('skip episode preserves mapped and unmapped entries across sources, including at completion',()=>{
 const first=assignSeasonStep({},source,source.files,episodes,0,1,new Set());
 const mapped=skipMappingStep(first.draft,0,episodes.length);assert.equal(mapped.index,1);assert.strictEqual(mapped.draft,first.draft);
 const gap=skipMappingStep(mapped.draft,2,episodes.length);assert.equal(gap.index,3);assert.equal(gap.draft.s2,undefined);assert.equal(gap.draft.s1a.file.id,1);
 const complete=skipMappingStep(gap.draft,episodes.length,episodes.length);assert.equal(complete.index,episodes.length);assert.strictEqual(complete.draft,first.draft);
});
