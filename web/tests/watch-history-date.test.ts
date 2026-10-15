import test from 'node:test';
import assert from 'node:assert/strict';
import {historyDate} from '../src/watch-history-date.ts';
const entry={kind:'ted_watched',observed_at:'2026-10-09T15:00:00Z',state:{played:true,position_ticks:0,play_count:1,last_played:null as string|null}};
test('TED undated history separates action receipt from a viewing date',()=>{
 assert.equal(historyDate(entry),`Viewing date unknown · Recorded ${new Date(entry.observed_at).toLocaleString()}`);
 const last_played='2026-10-09T14:00:00Z';
 assert.equal(historyDate({...entry,state:{...entry.state,last_played}}),`Watched ${new Date(last_played).toLocaleString()}`);
});
test('initial imports and later polls never present receipt or retained old dates as new viewing dates',()=>{
 for(const kind of ['stremio_import','stremio_observed']){
  const text=historyDate({...entry,kind,state:{...entry.state,last_played:'2020-01-01T00:00:00Z'}});
  assert.match(text,/Viewing date unknown/);
  assert.ok(text.startsWith(kind==='stremio_import'?'Imported ':'Detected '));
 }
});
