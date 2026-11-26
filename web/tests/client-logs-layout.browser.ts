// Uses the installed headless Chrome through CDP; no browser test dependency.
import test from 'node:test';
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdtemp,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {createServer} from 'vite';

test('rendered client log controls and responsive panels',async t=>{
 const profile=await mkdtemp(join(tmpdir(),'ted-logs-browser-'));
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
 for(const width of [1512,820,390]){
  await t.test(`${width}px filters, refresh control and retention stay aligned`,async()=>{
   await send('Emulation.setDeviceMetricsOverride',{width,height:1500,deviceScaleFactor:1,mobile:false},sessionId);
   await send('Page.navigate',{url:`http://127.0.0.1:${address.port}/tests/client-logs-layout-fixture.html`},sessionId);
   await evaluate(`new Promise((resolve,reject)=>{const deadline=Date.now()+10000;function ready(){if(document.querySelectorAll('.log-entry').length===3&&!document.querySelector('.logs-retention-fields').disabled){requestAnimationFrame(()=>requestAnimationFrame(resolve));}else if(Date.now()>deadline){reject(Error('Logs fixture failed to mount'));}else{setTimeout(ready,20);}}ready();})`);
   const metrics=await evaluate(`(()=>{
    const rect=e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height,right:r.right,bottom:r.bottom,cy:r.y+r.height/2};};
    const checkbox=document.querySelector('input[type=checkbox]'),label=checkbox.closest('label'),caption=document.createRange();caption.selectNode(label.lastChild);
    const cards=Array.from(document.querySelectorAll('.card')),fields=Array.from(document.querySelectorAll('.logs-filters label'),rect);
    const rows=[...new Set(fields.map(f=>f.y))].sort((a,b)=>a-b);
    return {pageWidth:document.documentElement.scrollWidth,viewport:innerWidth,checkbox:rect(checkbox),caption:rect(caption),
     cardGap:rect(cards[1]).y-rect(cards[0]).bottom,firstFieldGap:fields[0].y-rect(cards[0]).y,
     rowGap:rows[1]-Math.max(...fields.filter(f=>f.y===rows[0]).map(f=>f.bottom)),
     dates:Array.from(document.querySelectorAll('input[type=datetime-local]'),rect),
     retention:rect(document.querySelector('.logs-retention-fields input')),save:rect(document.querySelector('.logs-retention-fields button')),
     messages:Array.from(document.querySelectorAll('.log-entry pre'),rect),card:rect(cards[0])};
   })()`);
   assert.ok(metrics.pageWidth<=metrics.viewport,'the page must not scroll horizontally');
   assert.ok(metrics.checkbox.w>=16&&metrics.checkbox.w<=24&&metrics.checkbox.h<=24,'refresh uses a compact checkbox');
   assert.ok(Math.abs(metrics.checkbox.cy-metrics.caption.cy)<2,'checkbox and caption share a row');
   assert.ok(metrics.caption.x>=metrics.checkbox.right+6&&metrics.caption.x-metrics.checkbox.right<=16,'caption stays beside checkbox');
   assert.ok(metrics.cardGap>=20&&metrics.cardGap<=32,'query and retention cards have a visible gap');
   assert.ok(metrics.firstFieldGap<=32,'filters start at the card padding without extra form margins');
   assert.ok(metrics.rowGap>=12&&metrics.rowGap<=24,'filter rows have consistent compact spacing');
   for(const date of metrics.dates)assert.ok(date.w>=256,'date and time controls have room for their value');
   assert.ok(metrics.retention.w<=240,'retention is a compact number field');
   if(width>760)assert.ok(Math.abs(metrics.retention.bottom-metrics.save.bottom)<1,'retention field and save button align');
   else assert.ok(metrics.save.y>=metrics.retention.bottom&&Math.abs(metrics.save.x-metrics.retention.x)<1,'mobile save action wraps below its field');
   for(const message of metrics.messages)assert.ok(message.x>=metrics.card.x&&message.right<=metrics.card.right,'long messages stay inside the card');
   await evaluate(`document.querySelector('input[type=checkbox]').focus()`);
   await send('Input.dispatchKeyEvent',{type:'keyDown',key:' ',code:'Space',windowsVirtualKeyCode:32},sessionId);
   await send('Input.dispatchKeyEvent',{type:'keyUp',key:' ',code:'Space',windowsVirtualKeyCode:32},sessionId);
   assert.equal(await evaluate(`document.querySelector('input[type=checkbox]').checked`),true,'Space toggles auto refresh');
   await evaluate(`document.querySelector('[name=q]').value='buffer';document.querySelector('.logs-query-form').requestSubmit()`);
   await evaluate(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))`);
   assert.ok(await evaluate(`window.__logRequests.some(r=>r.path.includes('q=buffer'))`),'search still submits its filters');
   if(process.env.TED_LAYOUT_EVIDENCE){
    const shot=await send('Page.captureScreenshot',{format:'png',captureBeyondViewport:true},sessionId);
    await writeFile(join(process.env.TED_LAYOUT_EVIDENCE,`web-logs-${width}.png`),Buffer.from(shot.data,'base64'));
    await writeFile(join(process.env.TED_LAYOUT_EVIDENCE,`web-logs-${width}.json`),JSON.stringify(metrics,null,2));
   }
  });
 }
});
