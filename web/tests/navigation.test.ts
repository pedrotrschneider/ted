import {test} from 'node:test';
import assert from 'node:assert/strict';
import {pageFromHash} from '../src/navigation.ts';
test('each navigation URL restores its own view on reload',()=>{
 for(const page of ['movies','series','history','account','people','sources'])assert.equal(pageFromHash('#'+page,true),page);
});
test('back and forward between title bookmarks and top-level views select the correct section',()=>{
 assert.deepEqual(['#series/tt1224144','#history','#account','#movie/tt1345836','#history','#series/tt1224144'].map(h=>pageFromHash(h,false)),['series','history','account','movies','history','series']);
});
test('unknown routes and non-admin administration links never select admin views',()=>{
 for(const hash of ['','#unknown','#series-invalid','#people','#sources'])assert.equal(pageFromHash(hash,false),'movies');
});
