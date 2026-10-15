import test from 'node:test';import assert from 'node:assert/strict';
import {createRouteScroll} from '../src/route-scroll.ts';
test('opening titles and returning to lists reset the outgoing page offset',()=>{
 const history={scrollRestoration:'auto' as ScrollRestoration};let y=0;
 const route=createRouteScroll(history,()=>{y=0});
 assert.equal(history.scrollRestoration,'manual');
 for(const hash of ['#movies','#movie/tt0289043','#movies','#series','#series/tt14688458','#series']){
  y=8000;route.commit(hash);assert.equal(y,0,hash);
 }
 route.dispose();assert.equal(history.scrollRestoration,'auto');
});
test('loading and polling the same route leave deliberate scrolling untouched',()=>{
 const history={scrollRestoration:'auto' as ScrollRestoration};let y=0;
 const route=createRouteScroll(history,()=>{y=0});
 route.commit('#series/tt14688458');y=1400;
 for(let i=0;i<5;i++)route.commit('#series/tt14688458');
 assert.equal(y,1400);
 route.commit('#series');assert.equal(y,0);
});
test('back/forward traversal resets once per route and cleanup preserves prior policy',()=>{
 const history={scrollRestoration:'manual' as ScrollRestoration};let resets=0;
 const route=createRouteScroll(history,()=>{resets++});
 for(const hash of ['#movies','#movie/tt0289043','#movies','#movie/tt0289043','#movie/tt0289043'])route.commit(hash);
 assert.equal(resets,4);route.dispose();assert.equal(history.scrollRestoration,'manual');
});
