import {test} from 'node:test';
import assert from 'node:assert/strict';
import {watchIndicatorText} from '../src/title-status-api.ts';
test('series indicators show exact watched/total ratios including wide and single-episode counts', () => {
 for (const [watched,available] of [[1,150],[1234,5678],[0,1]]) {
  assert.equal(watchIndicatorText({seen:true,watched,available,progress:null},'series'),`${watched} / ${available}`);
 }
});
test('completion and movie progress retain icons instead of episode ratios', () => {
 assert.equal(watchIndicatorText({seen:true,watched:150,available:150,progress:null},'series'),'✓');
 assert.equal(watchIndicatorText({seen:true,watched:1,available:1,progress:null},'movie'),'✓');
 assert.equal(watchIndicatorText({seen:true,watched:0,available:1,progress:30},'movie'),'◷');
});
