// In-memory transport fixture; production host-file semantics have Go tests.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
export const hash=b=>createHash('sha256').update(b).digest('hex');
export class RecoveryFixture {
 snapshots=new Map(); blobs=new Map(); readFailures=new Set();
 async route(req,res){
  const m=req.url.match(/^\/integration\/recovery\/([a-f0-9-]+)(?:\/images\/([a-f0-9]+))?$/);if(!m)return false;
  const [,id,image]=m;if(req.method==='GET'&&this.readFailures.has(id)){res.writeHead(503).end();return true;}let data=Buffer.alloc(0);for await(const b of req)data=Buffer.concat([data,b]);
  if(image){const key=id+'/'+image;if(req.method==='PUT'){assert.equal(hash(data),image);this.blobs.set(key,data);res.writeHead(204).end()}else if(this.blobs.has(key)){res.end(this.blobs.get(key))}else res.writeHead(404).end();return true;}
  if(req.method==='GET'){const saved=this.snapshots.get(id);if(!saved){res.writeHead(404).end();return true}res.writeHead(200,{'Content-Type':'application/json',ETag:'"'+hash(Buffer.from(JSON.stringify(saved)))+'"'}).end(JSON.stringify(saved));return true;}
  const saved=JSON.parse(data);const previous=this.snapshots.get(id);const expected='"'+(previous?hash(Buffer.from(JSON.stringify(previous))):'')+'"';
  if(req.headers['if-match']!==expected||previous?.images.some(i=>!saved.images.some(n=>n.type===i.type&&n.index===i.index))){res.writeHead(409).end();return true;}
  for(const image of saved.images){const b=this.blobs.get(id+'/'+image.hash);assert.ok(b);assert.equal(hash(b),image.hash)}
  this.snapshots.set(id,saved);res.writeHead(204).end();return true;
 }
}
export async function runTask(api,key,expected='Completed'){
 const task=(await api('/ScheduledTasks')).find(t=>t.Key===key);assert.ok(task,'Scheduled task '+key);
 if(key==='TEDMetadataBackup')assert.ok(task.Triggers.some(t=>t.IntervalTicks===12000000000),'20-minute trigger');
 if(key==='TEDMetadataRestore')assert.equal(task.Triggers.length,0,'Restore must be manual');
 const before=task.LastExecutionResult?.EndTimeUtc;await api('/ScheduledTasks/Running/'+task.Id,'POST');
 for(let n=0;n<240;n++){await new Promise(r=>setTimeout(r,500));const state=await api('/ScheduledTasks/'+task.Id);if(state.State==='Idle'&&state.LastExecutionResult?.EndTimeUtc!==before){assert.equal(state.LastExecutionResult.Status,expected,JSON.stringify(state.LastExecutionResult));return}}
 throw Error('Metadata task did not finish within two minutes');
}
export async function metadataChecks({api,jf,user,token,item,seriesID,fixture,republish,remove}){
 const detail=()=>api(`/Users/${user}/Items/${item.Id}`);
 async function edit(name){const d=await detail();await api('/Items/'+item.Id,'POST',{...d,Name:name,Overview:'Saved descriptive metadata',Genres:['Animation'],Tags:['Recovery fixture'],LockData:true})}
 const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII=','base64');
 const r=await fetch(jf+`/Items/${item.Id}/Images/Primary`,{method:'POST',headers:{'X-Emby-Token':token,'Content-Type':'image/png'},body:png.toString('base64')});assert.ok(r.ok,'Upload fixture image');
 await edit('Saved custom episode title');
 await runTask(api,'TEDMetadataBackup');
 const original=structuredClone(fixture.snapshots.get(item.ExternalId));assert.ok(original?.images.length);assert.equal(original.metadata.fields.Name,'Saved custom episode title');
 const config=await api('/Plugins/87d58a79-d414-4d99-98a5-9ac7e080752c/Configuration');await api('/Plugins/87d58a79-d414-4d99-98a5-9ac7e080752c/Configuration','POST',{...config,MetadataRestoreId:seriesID});
 await edit('Temporary replacement');await api(`/Items/${item.Id}/Images/Primary/0`,'DELETE');
 await runTask(api,'TEDMetadataBackup','Failed');assert.deepEqual(fixture.snapshots.get(item.ExternalId),original,'Missing image retained prior snapshot');
 await runTask(api,'TEDMetadataRestore');const restored=await detail();assert.equal(restored.Name,'Saved custom episode title');assert.ok(restored.ImageTags.Primary);assert.equal(restored.Overview,'Saved descriptive metadata');assert.deepEqual(restored.Tags,['Recovery fixture']);assert.equal(restored.Id,item.Id);
 await runTask(api,'TEDMetadataRestore');assert.equal((await detail()).ImageTags.Primary,restored.ImageTags.Primary,'Idempotent restore');
 await edit('Newer edit stays');await republish(false);assert.equal((await detail()).Name,'Newer edit stays','Existing metadata must not be automatically replaced');
 const image=original.images[0],blobKey=item.ExternalId+'/'+image.hash,originalBytes=fixture.blobs.get(blobKey);
 await remove();
 fixture.blobs.set(blobKey,Buffer.from('corrupt'));
 await republish(true);assert.deepEqual(fixture.snapshots.get(item.ExternalId),original,'Failed automatic recovery preserves backup');
 fixture.blobs.set(blobKey,originalBytes);
 await republish(false);const automatic=await detail();assert.equal(automatic.Name,'Saved custom episode title');assert.ok(automatic.ImageTags.Primary);assert.equal(automatic.Id,item.Id,'Re-add retains identity');const list=await api(`/Shows/${automatic.SeriesId}/Episodes?seasonId=${automatic.SeasonId}&userId=${user}`);assert.ok(list.Items.some(e=>e.Id===item.Id),'Recovered episode must appear through the client season endpoint');
 await edit('Keep this on corrupt backup');fixture.blobs.set(blobKey,Buffer.from('corrupt'));
 await runTask(api,'TEDMetadataRestore','Failed');assert.equal((await detail()).Name,'Keep this on corrupt backup','Corrupt image prevented metadata application');
 console.log('PASS: metadata backup/restore, automatic re-add, preserved newer edits, failed-publication recovery, idempotence and corruption rejection.');
}
