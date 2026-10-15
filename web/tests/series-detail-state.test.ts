import test from 'node:test';
import assert from 'node:assert/strict';
import {seriesDetailState} from '../src/series-detail-state.ts';
import type {SeriesDetailState} from '../src/series-detail-state.ts';
const initial:SeriesDetailState={id:'tt0988824',admin:true,show:{id:'tt0988824',type:'series',name:'Show',year:'2007',description:'',poster:'',genres:[],episodes:[]},mapping:{category:'anime',mappings:[]},error:''};
test('batch-triggered refresh and failed reads retain the library label and progress mount data',()=>{
 let state=initial;
 for(let n=0;n<3;n++){
  state=seriesDetailState(state,{type:'refresh',id:initial.id,admin:true});
  assert.strictEqual(state.mapping,initial.mapping);assert.strictEqual(state.show,initial.show);
 }
 state=seriesDetailState(state,{type:'failed',id:initial.id,admin:true,error:'Connection lost'});
 assert.strictEqual(state.mapping,initial.mapping);assert.equal(state.mapping?.category,'anime');assert.equal(state.error,'Connection lost');
 const mapping={category:'anime' as const,mappings:[]};
 state=seriesDetailState(state,{type:'loaded',id:initial.id,admin:true,show:initial.show!,mapping});
 assert.strictEqual(state.mapping,mapping);assert.equal(state.error,'');
});
test('switching titles or losing admin access clears old data and rejects stale responses',()=>{
 for(const target of [{id:'tt1245698',admin:true},{id:initial.id,admin:false}]){
  const changed=seriesDetailState(initial,{type:'refresh',...target});
  assert.equal(changed.mapping,null);assert.equal(changed.show,null);
  assert.strictEqual(seriesDetailState(changed,{type:'loaded',id:initial.id,admin:true,show:initial.show!,mapping:initial.mapping}),changed);
  assert.strictEqual(seriesDetailState(changed,{type:'failed',id:initial.id,admin:true,error:'Old failure'}),changed);
 }
});
