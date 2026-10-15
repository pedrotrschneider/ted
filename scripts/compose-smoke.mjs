// Called by compose-smoke.sh inside Devbox; all credentials belong to its disposable stack.
import assert from 'node:assert/strict';
import {execFileSync, spawnSync} from 'node:child_process';
import {writeFileSync, mkdirSync} from 'node:fs';
import {setTimeout as delay} from 'node:timers/promises';
const project=process.env.TED_TEST_COMPOSE_PROJECT;
if(!project?.startsWith('ted-smoke-'))throw new Error('An owned smoke-test Compose project is required');
const args=['compose','--env-file','/dev/null','-f','compose.yaml','-p',project];
const docker=(...more)=>execFileSync('docker',[...args,...more],{encoding:'utf8'}).trim();
let base='http://'+docker('port','app','8080');
const password='disposable-smoke-password';
async function request(path,{method='GET',body,cookie,timeout=5000}={}){
 const r=await fetch(base+path,{method,headers:{'Content-Type':'application/json',...(cookie?{Cookie:cookie}:{})},body:body?JSON.stringify(body):undefined,signal:AbortSignal.timeout(timeout)});
 const text=await r.text();return {status:r.status,body:JSON.parse(text),cookie:r.headers.get('set-cookie')?.split(';')[0]};
}
async function ready(){
 const deadline=Date.now()+40000;
 while(Date.now()<deadline){
  try{if((await request('/health/ready')).status===200)return;}catch(error){if(!(error instanceof TypeError)&&error.name!=='TimeoutError')throw error;}
  await delay(250);
 }
 throw new Error('Stack did not return to ready after restart');
}
await ready();
const home=await fetch(base+'/');
assert.equal(home.status,200);assert.match(home.headers.get('content-type'),/text\/html/);
assert.match(home.headers.get('content-security-policy'),/script-src 'self'/);
const html=await home.text();
const assetPaths=[...html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)].map(match=>match[1]);
assert.ok(assetPaths.some(path=>path.endsWith('.js')));assert.ok(assetPaths.some(path=>path.endsWith('.css')));
for(const path of assetPaths){const asset=await fetch(base+path);assert.equal(asset.status,200);assert.ok((await asset.text()).length>0);assert.match(asset.headers.get('cache-control'),/immutable/);}
assert.equal((await fetch(base+'/third-party-notices.txt')).status,200);
assert.equal((await fetch(base+'/api/unknown')).status,404);
execFileSync('docker',[...args,'exec','-T','app','ted','bootstrap','admin'],{input:password,encoding:'utf8'});
const duplicate=spawnSync('docker',[...args,'exec','-T','app','ted','bootstrap','replacement'],{input:password,encoding:'utf8'});
assert.equal(duplicate.status,1);
const login=await request('/api/auth/login',{method:'POST',body:{username:'admin',password}});
assert.equal(login.status,200);assert.ok(login.cookie);
const initial=await request('/api/auth/me',{cookie:login.cookie});assert.equal(initial.status,200);
let movieMapping=null;
if(process.env.TED_MOVIE_LIVE_TEST==='1'){
 const options={cookie:login.cookie,timeout:15000}; // Provider-backed requests have a 10-second server deadline.
 const popular=await request('/api/movies',options);assert.equal(popular.status,200);assert.ok(popular.body.movies.length);assert.equal(popular.body.next_skip,50);
 const next=await request('/api/movies?skip=50',options);assert.equal(next.status,200);assert.ok(next.body.movies.length);assert.ok(next.body.movies.every(m=>!popular.body.movies.some(p=>p.id===m.id)));
 const search=await request('/api/movies?search=The%20Dark%20Knight%20Rises',options);assert.equal(search.status,200);assert.ok(search.body.movies.some(m=>m.id==='tt1345836'));
 const movie=await request('/api/movies/tt1345836',options);assert.equal(movie.status,200);assert.equal(movie.body.name,'The Dark Knight Rises');
 let torrent;
 for(let page=1;page<=100;page++){
  const response=await request('/api/admin/rd/torrents?page='+page,options);assert.equal(response.status,200);
  for(const item of response.body.torrents)if(item.name==='The.Dark.Knight.Rises.2012.RERiP.2160p.UHD.BluRay.x265-TERMiNAL'){assert.equal(torrent,undefined);torrent=item;}
  if(!response.body.next_page)break;
  if(page===100)throw new Error('Fixture scan exceeded its bound');
 }
 assert.ok(torrent,'Owner movie fixture must exist');
 const detail=await request('/api/admin/rd/torrents/'+torrent.id,options);assert.equal(detail.status,200);
 const files=detail.body.files.filter(f=>f.video&&!f.suggested_exclusion);assert.equal(files.length,1);
 const file=files[0];
 const selection={torrent_id:torrent.id,hash:detail.body.hash,file_id:file.id,path:file.path,bytes:file.bytes};
 const saved=await request('/api/admin/movies/tt1345836/mapping',{...options,method:'PUT',body:selection});assert.equal(saved.status,200);assert.equal(saved.body.status,'draft');assert.equal(saved.body.path,file.path);movieMapping=saved.body;
 const repeated=await request('/api/admin/movies/tt1345836/mapping',{...options,method:'PUT',body:selection});assert.equal(repeated.status,200);assert.deepEqual(repeated.body,movieMapping);
 const after=await request('/api/admin/rd/torrents/'+torrent.id,options);assert.equal(after.status,200);assert.deepEqual(after.body.files,detail.body.files);
}
async function checkMapping(){if(movieMapping){const r=await request('/api/admin/movies/tt1345836/mapping',{cookie:login.cookie});assert.equal(r.status,200);assert.deepEqual(r.body.mapping,movieMapping);}}
docker('restart','app');
// Docker may allocate a different host port when restarting a port-0 mapping.
base='http://'+docker('port','app','8080');await ready();
assert.deepEqual((await request('/api/auth/me',{cookie:login.cookie})).body,initial.body);
await checkMapping();
docker('restart','db');await ready();
assert.deepEqual((await request('/api/auth/me',{cookie:login.cookie})).body,initial.body);
await checkMapping();
assert.equal((await request('/api/auth/policy')).body.min_password_length,0);
const renamed=await request('/api/auth/profile',{method:'PUT',cookie:login.cookie,body:{display_name:'District Admin'}});assert.equal(renamed.status,200);assert.equal(renamed.body.display_name,'District Admin');
const created=await request('/api/admin/users',{method:'POST',cookie:login.cookie,body:{username:'viewer',display_name:'Viewer 猫',password:''}});assert.equal(created.status,201);
const listed=await request('/api/admin/users',{cookie:login.cookie});assert.equal(listed.status,200);assert.deepEqual(listed.body.users.map(user=>user.username),['admin','viewer']);
assert.equal((await request('/api/admin/users',{method:'POST',body:{username:'unauthorized',password}})).status,401);
const blankLogin=await request('/api/auth/login',{method:'POST',body:{username:'viewer',password:''}});assert.equal(blankLogin.status,200);
const originalConfig=process.env.TED_CONFIG_DIR;
const restoredConfig=originalConfig+'-relocated';mkdirSync(restoredConfig,{recursive:true});
docker('down');
execFileSync('docker',['run','--rm','--network','none','--entrypoint','sh','--mount',`type=bind,src=${originalConfig},dst=/source,readonly`,'--mount',`type=bind,src=${restoredConfig},dst=/var/lib/postgresql`,'postgres:18','-c','cp -a /source/. /var/lib/postgresql/'],{stdio:'inherit'});
process.env.TED_CONFIG_DIR=restoredConfig;
docker('up','-d','--wait');base='http://'+docker('port','app','8080');await ready();
assert.equal((await request('/api/auth/me',{cookie:login.cookie})).body.display_name,'District Admin');
assert.equal((await request('/api/auth/me',{cookie:blankLogin.cookie})).body.display_name,'Viewer 猫');
await checkMapping();
const mounts=JSON.parse(execFileSync('docker',['inspect',docker('ps','-q','db'),'--format','{{json .Mounts}}'],{encoding:'utf8'}));
assert.ok(mounts.every(mount=>mount.Type==='bind'));assert.ok(mounts.some(mount=>mount.Source===restoredConfig+'/postgres'));
assert.equal((await request('/api/auth/logout',{method:'POST',cookie:login.cookie})).status,200);
assert.equal((await request('/api/auth/me',{cookie:login.cookie})).status,401);
const evidence={time:new Date().toISOString(),checks:['local Config copy and fresh containers retain both sessions and display names', 'blank-password login', 'no database Docker volumes', 'built HTML, JavaScript, CSS, notices and API boundaries', 'administrator lists local users', 'fresh bootstrap','repeat bootstrap refused','login','app restart retains session','database restart retains session','administrator creates local user','unauthenticated creation denied','logout revokes session'],postgres:docker('exec','-T','db','postgres','--version'),go:execFileSync('go',['version'],{encoding:'utf8'}).trim(),image:docker('images','-q','app')};
if(movieMapping)evidence.checks.push('Live Cinemeta movie search/details and explicit RD root-MKV mapping', 'idempotent mapping save with remote file selections unchanged', 'mapping and stable TED identity survive app/database restart and relocated Config copy');
writeFileSync(process.env.TED_TEST_EVIDENCE+'/compose-smoke.json',JSON.stringify(evidence,null,2)+'\n');
console.log(JSON.stringify(evidence,null,2));
