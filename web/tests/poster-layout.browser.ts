// Uses the installed headless Chrome through CDP; no browser test dependency.
import test from 'node:test';
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdtemp,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {createServer} from 'vite';

test('rendered poster indicators in grids and details',async t=>{
 const profile=await mkdtemp(join(tmpdir(),'ted-poster-browser-'));
 const server=await createServer({root:resolve('.'),server:{host:'127.0.0.1',port:0}});
 await server.listen();
 const address=server.httpServer!.address();assert(address&&typeof address!=='string');
 const browser=spawn(process.env.TED_TEST_BROWSER??(process.platform==='darwin'?'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome':'google-chrome'),[
  '--headless=new','--disable-gpu','--no-first-run','--no-default-browser-check','--remote-debugging-port=0',`--user-data-dir=${profile}`,'about:blank',
 ],{stdio:['ignore','ignore','pipe']});
 let socket:WebSocket|undefined;
 t.after(async()=>{socket?.close();browser.kill();await server.close();await rm(profile,{recursive:true,force:true,maxRetries:5});});
 const endpoint=await new Promise<string>((ok,fail)=>{
  const timer=setTimeout(()=>fail(Error('Headless Chrome did not expose CDP')),15000);let stderr='';
  browser.on('error',fail);browser.stderr.on('data',chunk=>{stderr+=String(chunk);const match=stderr.match(/DevTools listening on (ws:\/\/[^\s]+)/);if(match){clearTimeout(timer);ok(match[1]);}});
 });
 socket=new WebSocket(endpoint);
 await new Promise<void>((ok,fail)=>{socket!.addEventListener('open',()=>ok(),{once:true});socket!.addEventListener('error',()=>fail(Error('CDP connection failed')),{once:true});});
 let sequence=0;
 const pending=new Map<number,{ok:(value:any)=>void;fail:(error:Error)=>void}>();
 socket.addEventListener('message',event=>{const message=JSON.parse(String(event.data));const task=pending.get(message.id);if(task){pending.delete(message.id);if(message.error)task.fail(Error(JSON.stringify(message.error)));else task.ok(message.result);}});
 const send=(method:string,params:Record<string,unknown>={},sessionId?:string)=>new Promise<any>((ok,fail)=>{
  const id=++sequence;const timer=setTimeout(()=>{pending.delete(id);fail(Error(`CDP timeout: ${method}`));},15000);
  pending.set(id,{ok:value=>{clearTimeout(timer);ok(value);},fail:error=>{clearTimeout(timer);fail(error);}});
  socket!.send(JSON.stringify({id,method,params,sessionId}));
 });
 const {targetId}=await send('Target.createTarget',{url:'about:blank'});
 const {sessionId}=await send('Target.attachToTarget',{targetId,flatten:true});
 const evaluate=async(expression:string)=>{
  const r=await send('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true},sessionId);
  assert.equal(r.exceptionDetails,undefined,JSON.stringify(r.exceptionDetails));return r.result.value;
 };
 await send('Page.enable',{},sessionId);
 for(const width of [1100,390]){
  await t.test(`${width}px layout centers and contains saved, personal, joint and person icons`,async()=>{
   await send('Emulation.setDeviceMetricsOverride',{width,height:1500,deviceScaleFactor:1,mobile:false},sessionId);
   await send('Page.navigate',{url:`http://127.0.0.1:${address.port}/tests/poster-layout-fixture.html`},sessionId);
   await evaluate(`new Promise((resolve,reject)=>{const deadline=Date.now()+10000;function ready(){if(document.querySelectorAll('.other-user-avatar').length===8){requestAnimationFrame(()=>requestAnimationFrame(resolve));}else if(Date.now()>deadline){reject(Error('Poster fixture failed to mount'));}else{setTimeout(ready,20);}}ready();})`);
   const metrics=await evaluate(`Array.from(document.querySelectorAll('.title-poster'),poster=>{
    const rect=e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height,cx:r.x+r.width/2,cy:r.y+r.height/2,right:r.right,bottom:r.bottom};};
    const symbol=e=>{const svg=e.querySelector('svg'),ink=svg.getBBox();return {name:e.getAttribute('aria-label'),badge:rect(e),icon:rect(svg),color:getComputedStyle(svg).color,
     ink:{x:ink.x,y:ink.y,w:ink.width,h:ink.height},paths:Array.from(svg.querySelectorAll('path'),rect)};};
    const avatar=poster.querySelector('.other-user-avatar'),range=document.createRange();range.selectNodeContents(avatar);
    return {saved:symbol(poster.querySelector('.watchlist-badge')),own:symbol(poster.querySelector('.poster-personal-rating .personal-rating')),
     peer:symbol(poster.querySelector('.other-user-watch .personal-rating')),joint:symbol(poster.querySelector('.joint-watch .personal-rating')),
     poster:rect(poster),person:rect(poster.querySelector('.other-user-watch')),jointWatch:rect(poster.querySelector('.joint-watch')),avatar:rect(avatar),initial:rect(range)};
   })`);
   assert.equal(metrics.length,8);
   for(const m of metrics){
    for(const value of [m.saved,m.own,m.peer,m.joint]){
     assert.ok(Math.abs(value.badge.cx-value.icon.cx)<.6,'symbol must be horizontally centered');
     assert.ok(Math.abs(value.badge.cy-value.icon.cy)<.6,'symbol must be vertically centered');
     assert.ok(value.icon.w>=6&&value.icon.w>value.badge.w*.6,'symbol must fill the badge');
     assert.ok(value.icon.right<=value.badge.right&&value.icon.bottom<=value.badge.bottom,'symbol must stay inside its circle');
     assert.equal(value.color,'rgb(255, 255, 255)');
     assert.ok(value.badge.x>=m.poster.x&&value.badge.right<=m.poster.right,'rating stays within the artwork');
     assert.ok(value.badge.y>=m.poster.y&&value.badge.bottom<=m.poster.bottom,'rating stays within the artwork');
     if(value.name==='Absolute cinema'){
      assert.ok(value.ink.h<17&&value.ink.h>14,'palms leave room above and below');
      assert.equal(value.paths.length,2);
      assert.ok(value.paths[1].x-value.paths[0].right>value.icon.w*.1,'palms have a visible horizontal gap');
      for(const path of value.paths)assert.ok(Math.abs(path.w/path.h-.5)<.01,'palms retain their proportions');
     }
    }
    assert.ok(Math.abs(m.peer.badge.w/m.person.h-.69)<.01,'person rating is 50% larger than the previous 46% size');
    assert.ok(m.peer.badge.right>m.avatar.right&&m.peer.badge.right-m.avatar.right<m.person.h*.25,'rating stays at the avatar corner when progress widens its pill');
    assert.ok(m.peer.badge.y<m.avatar.y&&m.avatar.y-m.peer.badge.y<m.person.h*.25);
    assert.ok(Math.hypot(m.peer.badge.cx-m.avatar.cx,m.peer.badge.cy-m.avatar.cy)>m.peer.badge.w/2,'rating leaves the avatar centre visible');
    assert.ok(Math.abs(m.joint.badge.w/m.jointWatch.h-.69)<.01,'joint rating uses the same larger size');
    assert.ok(Math.abs(m.initial.cx-m.avatar.cx)<1,'initial must be horizontally centered');
    assert.ok(Math.abs(m.initial.cy-m.avatar.cy)<3,'initial must be vertically centered');
   }
   if(process.env.TED_LAYOUT_EVIDENCE){
    const shot=await send('Page.captureScreenshot',{format:'png',captureBeyondViewport:true},sessionId);
    await writeFile(join(process.env.TED_LAYOUT_EVIDENCE,`web-posters-${width}.png`),Buffer.from(shot.data,'base64'));
   }
  });
 }
});
