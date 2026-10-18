import test from 'node:test';
import assert from 'node:assert/strict';
import {peopleWatchLayout} from '../src/people-watch-layout.ts';

test('people stack at normal size, then shrink together before hiding anyone',()=>{
 assert.deepEqual(peopleWatchLayout(3,120,30,24,5),{visible:3,hidden:0,height:30,offset:0});
 assert.deepEqual(peopleWatchLayout(3,91,30,24,5),{visible:3,hidden:0,height:27,offset:0});
});
test('overflow reserves a readable final row and counts only hidden people',()=>{
 const layout=peopleWatchLayout(7,85,30,24,5);
 assert.deepEqual(layout,{visible:2,hidden:5,height:25,offset:0});
 assert.equal((layout.visible+1)*layout.height+layout.visible*5,85);
 assert.deepEqual(peopleWatchLayout(7,30,30,24,5),{visible:0,hidden:7,height:30,offset:0});
});
test('tiny artwork keeps a whole overflow badge and empty lists add no overflow',()=>{
 assert.deepEqual(peopleWatchLayout(4,10,30,24,5),{visible:0,hidden:4,height:24,offset:-14});
 assert.deepEqual(peopleWatchLayout(0,0,30,24,5),{visible:0,hidden:0,height:30,offset:0});
 for(let count=1;count<25;count++)for(let available=24;available<400;available++){
  const fit=peopleWatchLayout(count,available,30,24,5),rows=fit.visible+(fit.hidden>0?1:0);
  assert.equal(fit.visible+fit.hidden,count);
  assert.ok(fit.height>=24&&fit.height<=30);
  assert.ok(rows*fit.height+Math.max(0,rows-1)*5<=available+1e-8);
 }
});
