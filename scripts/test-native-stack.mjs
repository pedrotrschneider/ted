// Run through Devbox. Uses only its own Compose project and host directories;
// retains fixture files/logs and tears down its containers even on failure.
// --preview keeps a successful generated-media stack for owner client checks.
import assert from 'node:assert/strict';
import {execFile} from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {randomUUID} from 'node:crypto';

const preview=process.argv.includes('--preview');
assert.ok(process.argv.slice(2).every(arg=>arg==='--preview'),'Only --preview is supported');
const project=`ted-native-test-${process.pid}${preview?'-'+randomUUID().slice(0,8):''}`;
const evidence=path.resolve('.devbox/test-evidence',`${new Date().toISOString().replaceAll(':','-')}-${project}`);
fs.mkdirSync(evidence,{recursive:true});
for(const dir of ['Config/postgres','Config/metadata','Media'])fs.mkdirSync(path.join(evidence,dir),{recursive:true});
const fixtureEnv=path.join(evidence,'fixture.env');
fs.writeFileSync(fixtureEnv,'# Disposable fixture; no production credentials.\n',{mode:0o600});
const env={...process.env,TED_DB_PASSWORD:'native-fixture-only',TED_CONFIG_DIR:path.join(evidence,'Config'),TED_MEDIA_DIR:path.join(evidence,'Media'),TED_PORT:'0',TED_BIND_ADDRESS:'127.0.0.1',TED_UID:String(os.userInfo().uid),TED_GID:String(os.userInfo().gid),TED_INSECURE_HTTP:'true',TED_MIN_PASSWORD_LENGTH:'0',TED_REAL_DEBRID_API_KEY:'',TED_ACCESS_TOKEN_TTL:'10m',TED_REFRESH_TOKEN_IDLE_TTL:'720h',TED_TOKEN_FAMILY_TTL:'2160h'};
const compose=['compose','-p',project,'--env-file',fixtureEnv,'-f',path.resolve('compose.yaml')];
// Hold PostgreSQL's first-run init hook to verify readiness against its temporary
// socket-only bootstrap server. Only this unique disposable stack is affected.
const gate=path.join(evidence,'database-startup-gate');
fs.mkdirSync(gate,{mode:0o777});fs.chmodSync(gate,0o777);
const initScript=path.join(gate,'hold.sh');
fs.writeFileSync(initScript,'#!/bin/sh\nset -eu\ntouch /test-gate/entered\nwhile [ ! -f /test-gate/release ]; do sleep 1; done\n',{mode:0o755});
const startupOverride=path.join(evidence,'startup.compose.json');
fs.writeFileSync(startupOverride,JSON.stringify({services:{db:{volumes:[
 {type:'bind',source:initScript,target:'/docker-entrypoint-initdb.d/hold.sh',read_only:true},
 {type:'bind',source:gate,target:'/test-gate'},
]}}},null,2)+'\n');
compose.push('-f',startupOverride);
let commandNumber=0;
let previewReady=false;
function run(executable,args,input){
 return new Promise((resolve,reject)=>{
  const number=++commandNumber;
  const child=execFile(executable,args,{env,encoding:'utf8',timeout:600000,maxBuffer:16<<20},(error,stdout,stderr)=>{
   fs.writeFileSync(path.join(evidence,`command-${number}.log`),(stdout??'')+(stderr??''),{mode:0o600});
   if(error)reject(new Error(`Fixture command failed; see command-${number}.log`,{cause:error}));
   else resolve(stdout.trim());
  });
  child.stdin.end(input??'');
 });
}
function stack(args,input){return run('docker',[...compose,...args],input)}
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
try{
 console.log(`Building isolated native stack; evidence: ${evidence}`);
 await stack(['build','app']);
 await stack(['up','-d','db']);
 const deadline=Date.now()+60000;
 while(!fs.existsSync(path.join(gate,'entered'))&&Date.now()<deadline)await delay(200);
 assert.ok(fs.existsSync(path.join(gate,'entered')),'Database init hook must enter before the readiness regression');
 await stack(['exec','-T','db','pg_isready','-U','ted','-d','ted']);
 const tcpAccepted=await stack(['exec','-T','-e','PGPASSWORD=native-fixture-only','db','psql','-h','127.0.0.1','-U','ted','-d','ted','-XAtqc','SELECT 1']).then(()=>true,()=>false);
 assert.equal(tcpAccepted,false,'Bootstrap socket readiness must not authorize application startup');
 const dbContainer=await stack(['ps','-q','db']);
 const health=await run('docker',['inspect','--format','{{.State.Health.Status}}',dbContainer]);
 assert.notEqual(health,'healthy','Production health check must reject the bootstrap-only database');
 fs.writeFileSync(path.join(evidence,'database-startup-check.json'),JSON.stringify({socket_probe:'accepted',tcp_sql_probe:'rejected',health},null,2)+'\n');
 fs.writeFileSync(path.join(gate,'release'),'release\n');
 await stack(['up','-d','--wait','--wait-timeout','120']);
 const address=await stack(['port','app','8080']);
 assert.match(address,/^127\.0\.0\.1:\d+$/);
 const base=`http://${address}`;
 const ready=await fetch(`${base}/health/ready`);
 assert.equal(ready.status,200);
 assert.deepEqual(await ready.json(),{status:'ready'});
 await stack(['exec','-T','app','ted','bootstrap','native_tester'],'native-test-password\n');
 const login=await fetch(`${base}/api/auth/login`,{method:'POST',headers:{Origin:base,'Content-Type':'application/json'},body:JSON.stringify({username:'native_tester',password:'native-test-password'})});
 assert.equal(login.status,200);
 const cookie=login.headers.getSetCookie().map(value=>value.split(';')[0]).join('; ');
 assert.ok(cookie.includes('ted_session='));
 const providerURL=`${base}/api/admin/providers`;
 const providers=await fetch(providerURL,{headers:{Cookie:cookie}});assert.equal(providers.status,200);
 const providerStatus=await providers.json();assert.equal(providerStatus.rd_configured,false);
 const savedProviders=await fetch(providerURL,{method:'PUT',headers:{Cookie:cookie,Origin:base,'Content-Type':'application/json'},body:JSON.stringify({revision:providerStatus.revision,rd_key:'fixture-rd-key',tmdb_token:'fixture-tmdb-token',tvdb_key:'fixture-tvdb-key'})});
 assert.equal(savedProviders.status,200);const savedStatus=await savedProviders.json();
 assert.equal(savedStatus.rd_configured,true);assert.equal(savedStatus.tmdb_configured,true);assert.equal(savedStatus.tvdb_configured,true);
 assert.ok(!JSON.stringify(savedStatus).includes('fixture-'));
 const clearedProviders=await fetch(providerURL,{method:'PUT',headers:{Cookie:cookie,Origin:base,'Content-Type':'application/json'},body:JSON.stringify({revision:savedStatus.revision,rd_key:'',tmdb_token:'',tvdb_key:''})});
 assert.equal(clearedProviders.status,200);const clearedStatus=await clearedProviders.json();
 assert.equal(clearedStatus.rd_configured,false);assert.equal(clearedStatus.tmdb_configured,false);assert.equal(clearedStatus.tvdb_configured,false);
 for(const route of ['streaming','mapping-operation']){
  const response=await fetch(`${base}/api/admin/movies/tt1345836/${route}`,{headers:{Cookie:cookie}});
  assert.equal(response.status,200);
  const state=await response.json();
  assert.equal(state.publication_mode,'native');
  assert.equal(state.phase,route==='streaming'?'draft':'');
 }
 assert.equal((await fetch(`${base}/integration/catalog`,{headers:{Authorization:'Bearer '}})).status,503);
 const mediaId='11111111-1111-4111-8111-111111111111';
 const mappingId='22222222-2222-4222-8222-222222222222';
 const directory=path.join(evidence,'Media',mediaId);
 fs.mkdirSync(directory,{mode:0o700});
 const file=path.join(directory,'1.media');
 await run('ffmpeg',['-v','error','-f','lavfi','-i','testsrc2=size=128x72:rate=10','-f','lavfi','-i','sine=frequency=440:sample_rate=48000','-t',preview?'90':'20','-c:v','libx264','-c:a','aac','-f','matroska',file]);
 const probe=await run('ffprobe',['-v','error','-show_entries','format=format_name,duration,size:stream=index,codec_type,codec_name','-of','json',file]);
 const bytes=fs.statSync(file).size;
 // Controlled fixture constants and ffprobe JSON only; owner data is never read.
 await stack(['exec','-T','db','psql','-v','ON_ERROR_STOP=1','-U','ted','-d','ted'],`
 INSERT INTO catalog_media(id,cinemeta_id,metadata) VALUES('${mediaId}','tt1234567','{"id":"tt1234567","name":"Native generated fixture"}');
 INSERT INTO media_mappings(id,movie_id,rd_account_id,torrent_id,info_hash,file_id,file_path,file_bytes,source_snapshot,created_by)
 VALUES('${mappingId}','${mediaId}','fixture-account','FIXTURE',repeat('a',40),1,'/fixture.mkv',${bytes},'{}',1);
 INSERT INTO streaming_items(movie_id,mapping_id,phase,published,probe) VALUES('${mediaId}','${mappingId}','ready',true,'${probe}');
 INSERT INTO local_files(movie_id,generation,job_id,phase,total_bytes,completed_bytes,validated_bytes,available) VALUES('${mediaId}',1,0,'ready',${bytes},${bytes},${bytes},true);
 `);
 async function native(origin,route,token,body){
  return fetch(`${origin}/api/client/v1/${route}`,{method:body===undefined?'GET':'POST',headers:{...(token?{Authorization:`Bearer ${token}`}:{ }),'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});
 }
 const device=randomUUID();
 const signedIn=await native(base,'auth/login','',{username:'native_tester',password:'native-test-password',device_id:device,device_name:'Compose native fixture'});
 assert.equal(signedIn.status,200);
 assert.match(signedIn.headers.get('cache-control'),/no-store/);
 const tokens=await signedIn.json();
 const mappedSources=await native(base,'playback/sources',tokens.access_token,{media:{kind:'movie',id:'tt1234567'}});
 assert.equal(mappedSources.status,200);
 const mappedPolicy=await mappedSources.json();
 assert.equal(mappedPolicy.mode,'mapped');assert.equal(mappedPolicy.source,'local');assert.equal(mappedPolicy.mapped_ready,true);assert.deepEqual(mappedPolicy.sources,[]);
 const unlinkedAddons=await native(base,'addons',tokens.access_token);
 assert.equal(unlinkedAddons.status,409);assert.equal((await unlinkedAddons.json()).code,'stremio_not_linked');
 const unmappedSources=await native(base,'playback/sources',tokens.access_token,{media:{kind:'episode',id:'tt0386676:1:4'}});
 assert.equal(unmappedSources.status,409);assert.equal((await unmappedSources.json()).code,'stremio_not_linked');
 const titles=await native(base,'titles/mapped',tokens.access_token);
 assert.equal(titles.status,200);
 assert.equal((await titles.json()).titles[0].media.id,'tt1234567');
 const created=await native(base,'playback/sessions',tokens.access_token,{media:{kind:'movie',id:'tt1234567'}});
 assert.equal(created.status,201);
 const session=await created.json();
 const range=await fetch(`${base}${session.media_url}`,{headers:{Authorization:`Bearer ${tokens.access_token}`,Range:'bytes=100-199'}});
 assert.equal(range.status,206);
 assert.ok(Buffer.from(await range.arrayBuffer()).equals(fs.readFileSync(file).subarray(100,200)),'Media must retain original bytes');
 const resolved=await native(base,'downloads/resolve',tokens.access_token,{session_id:session.id});
 assert.equal(resolved.status,200);const download=await resolved.json();
 assert.equal(download.bytes,fs.statSync(file).size);
 const downloaded=[];
 for(const [start,end] of [[0,Math.floor(download.bytes/2)-1],[Math.floor(download.bytes/2),download.bytes-1]]){
  const part=await fetch(`${base}${download.media_url}`,{headers:{Authorization:`Bearer ${tokens.access_token}`,Range:`bytes=${start}-${end}`,'If-Match':download.etag}});
  assert.equal(part.status,206);assert.equal(part.headers.get('etag'),download.etag);
  assert.equal(part.headers.get('content-range'),`bytes ${start}-${end}/${download.bytes}`);
  downloaded.push(Buffer.from(await part.arrayBuffer()));
 }
 assert.ok(Buffer.concat(downloaded).equals(fs.readFileSync(file)),'Resumed device download must retain the entire original file');
 assert.equal((await fetch(`${base}${download.media_url}`,{headers:{Authorization:`Bearer ${tokens.access_token}`,'If-Match':'"changed"'}})).status,409);
 const now=new Date().toISOString();
 const events=[{id:randomUUID(),session_id:session.id,sequence:1,media:session.media,kind:'start',position_ms:0,duration_ms:session.duration_ms,observed_at:now,origin:'live',natural_eof:false},
  {id:randomUUID(),session_id:session.id,sequence:2,media:session.media,kind:'stop',position_ms:7000,duration_ms:session.duration_ms,observed_at:now,origin:'live',natural_eof:false}];
 for(const duplicate of [false,true]){
  const report=await native(base,'playback/events',tokens.access_token,{events});
  assert.equal(report.status,200);
  assert.ok((await report.json()).events.every(event=>event.duplicate===duplicate),'Report replay must retain receipts');
 }
 const rotated=await native(base,'auth/refresh','',{refresh_token:tokens.refresh_token});
 assert.equal(rotated.status,200);
 const replacement=await rotated.json();
 assert.ok(replacement.family_id===tokens.family_id,'Refresh must preserve family identity');
 assert.ok(replacement.refresh_token!==tokens.refresh_token,'Refresh must consume the prior token');
 assert.equal(await stack(['exec','-T','db','psql','-U','ted','-d','ted','-Atc',"SELECT count(*) FROM native_refresh_tokens WHERE consumed_at IS NULL"]),'1');
 let workers=false;
 for(let attempt=0;attempt<30;attempt++){
  const count=await stack(['exec','-T','db','psql','-U','ted','-d','ted','-Atc',"SELECT count(DISTINCT kind) FROM river_job WHERE kind IN ('stremio_dispatch','catalog_metadata_dispatch','orphan_cleanup') AND state='completed'"]);
  if(count==='3'){workers=true;break}
  await delay(1000);
 }
 assert.ok(workers,'Core workers must run without plugin/RD keys');
 console.log('Native startup, browser sign-in, readiness and periodic workers passed; checking restart.');
 await stack(['restart','app']);
 await stack(['up','-d','--no-recreate','--wait','--wait-timeout','60','app']);
 // Docker may reassign a dynamic host port on restart. Read its actual binding;
 // production uses a configured fixed port, unlike this disposable fixture.
 const restartedAddress=await stack(['port','app','8080']);
 assert.match(restartedAddress,/^127\.0\.0\.1:\d+$/);
 console.log(`Restart address: ${address} → ${restartedAddress}`);
 const after=await fetch(`http://${restartedAddress}/api/admin/movies/tt1345836/mapping-operation`,{headers:{Cookie:cookie}});
 assert.equal(after.status,200);
 assert.equal((await after.json()).publication_mode,'native');
 const restartedBase=`http://${restartedAddress}`;
 const me=await native(restartedBase,'auth/me',replacement.access_token);
 assert.equal(me.status,200);
 assert.ok((await me.json()).family_id===tokens.family_id,'Restart must retain native family');
 const state=await native(restartedBase,'watch-state/movie/tt1234567',replacement.access_token);
 assert.equal(state.status,200);
 const current=await state.json();
 assert.equal(current.position_ms,7000);
 assert.equal(current.played,false);
 // T3 personal rows and episode browsing use durable catalog snapshots without
 // creating a shared mapping for the watched/unmapped show.
 const showId='tt7654321';
 const episodeId=`${showId}:1:1`;
 await stack(['exec','-T','db','psql','-v','ON_ERROR_STOP=1','-U','ted','-d','ted'],`
 INSERT INTO native_catalog_titles(kind,title_id,metadata,observed_at) VALUES('series','${showId}','{"id":"${showId}","type":"series","name":"Generated schedule fixture"}',now());
 INSERT INTO catalog_episode_schedules(title_id,episodes,observed_at,fetched_at) VALUES('${showId}',
 '[{"id":"${showId}:0:1","season":0,"number":1,"name":"Special","release_date":"2000-01-01"},
 {"id":"${episodeId}","season":1,"number":1,"name":"One","release_date":"2000-01-01"},
 {"id":"${showId}:1:2","season":1,"number":2,"name":"Unknown"},
 {"id":"${showId}:1:3","season":1,"number":3,"name":"Next released","release_date":"2000-01-01"},
 {"id":"${showId}:1:4","season":1,"number":4,"name":"Future","release_date":"2100-01-01"}]',now(),now());
 `);
 const mark=await native(restartedBase,'watch-state',replacement.access_token,{media:{kind:'episode',id:episodeId},revision:0,action:'watched'});
 assert.equal(mark.status,200);
 const cw=await native(restartedBase,'rows/continue-watching',replacement.access_token);
 assert.equal(cw.status,200);
 const cwPage=await cw.json();
 assert.equal(cwPage.titles.length,1);
 assert.equal(cwPage.titles[0].watch_state.position_ms,7000);
 const next=await native(restartedBase,'rows/next-up',replacement.access_token);
 assert.equal(next.status,200);
 const nextPage=await next.json();
 assert.equal(nextPage.titles[0].media.id,`${showId}:1:3`);
 assert.equal(nextPage.titles[0].mapped_ready,false);
 assert.equal(nextPage.issues.length,0);
 const episodes=await native(restartedBase,`titles/series/${showId}/episodes?season=0`,replacement.access_token);
 assert.equal(episodes.status,200);
 assert.equal((await episodes.json()).titles[0].media.id,`${showId}:0:1`);
 assert.equal(await stack(['exec','-T','db','psql','-U','ted','-d','ted','-Atc','SELECT count(*) FROM media_mappings']),'1');
 fs.writeFileSync(path.join(evidence,'native-catalog-check.json'),JSON.stringify({continue_watching:cwPage,next_up:nextPage,shared_mappings:1},null,2)+'\n',{mode:0o600});
 const resume=await native(restartedBase,'playback/sessions',replacement.access_token,{media:session.media});
 assert.equal(resume.status,201);
 assert.equal((await resume.json()).resume_ms,7000);
 // Device-created historical sessions survive server restart and do not need
 // an active streaming session or a shared library mapping.
 const offlineId=randomUUID(),offlineMedia={kind:'movie',id:'tt2345678'},offlineAt=new Date().toISOString();
 const offlineRegistration={id:offlineId,media:offlineMedia,watch_revision:0,started_at:offlineAt};
 const offlineEvents=[{id:randomUUID(),session_id:offlineId,sequence:1,media:offlineMedia,kind:'start',position_ms:0,duration_ms:100000,observed_at:offlineAt,origin:'offline',natural_eof:false},
  {id:randomUUID(),session_id:offlineId,sequence:2,media:offlineMedia,kind:'stop',position_ms:25000,duration_ms:100000,observed_at:offlineAt,origin:'offline',natural_eof:false}];
 for(const duplicate of [false,true]){
  const report=await native(restartedBase,'playback/events',replacement.access_token,{sessions:[offlineRegistration],events:offlineEvents});
  assert.equal(report.status,200);assert.ok((await report.json()).events.every(e=>e.duplicate===duplicate));
 }
 const offlineState=await native(restartedBase,'watch-state/movie/tt2345678',replacement.access_token);
 const savedOffline=await offlineState.json();assert.equal(savedOffline.position_ms,25000);assert.equal(savedOffline.played,false);
 assert.equal(await stack(['exec','-T','db','psql','-U','ted','-d','ted','-Atc','SELECT count(*) FROM media_mappings']),'1');
 fs.writeFileSync(path.join(evidence,'native-offline-check.json'),JSON.stringify({original_download_bytes:download.bytes,resumed_file_matches:true,changed_validator_status:409,offline_state:savedOffline,replay_duplicates:true,shared_mappings:1},null,2)+'\n',{mode:0o600});
 assert.equal((await native(restartedBase,'auth/logout',replacement.access_token,{})).status,200);
 assert.equal((await native(restartedBase,'auth/me',replacement.access_token)).status,401);
 assert.equal((await native(restartedBase,'auth/refresh','',{refresh_token:replacement.refresh_token})).status,401);
 console.log('Native Compose authentication, original-byte ranges, replay, rotation, restart/resume, personal rows, unmapped episode state and revocation passed. Only generated fixture media/catalog data was used.');
 if(preview){
  // Complete API checks with the normal TTL, then use a short TTL and a longer
  // video for the owner's native-player refresh/seek check. Never change .env.
  const settings=['TED_DB_PASSWORD','TED_CONFIG_DIR','TED_MEDIA_DIR','TED_PORT','TED_BIND_ADDRESS','TED_UID','TED_GID','TED_INSECURE_HTTP','TED_MIN_PASSWORD_LENGTH','TED_REAL_DEBRID_API_KEY','TED_ACCESS_TOKEN_TTL','TED_REFRESH_TOKEN_IDLE_TTL','TED_TOKEN_FAMILY_TTL'];
  fs.writeFileSync(fixtureEnv,'# Disposable local fixture; never use production credentials.\n'+settings.map(key=>`${key}=${JSON.stringify(env[key])}`).join('\n')+'\n',{mode:0o600});
  const override=path.join(evidence,'preview.compose.json');
  fs.writeFileSync(override,JSON.stringify({services:{app:{restart:'no',environment:{TED_ACCESS_TOKEN_TTL:'30s'}},db:{restart:'no'}}},null,2)+'\n',{mode:0o600});
  compose.push('-f',override);
  await stack(['up','-d','--no-build','--wait','--wait-timeout','120']);
  const previewAddress=await stack(['port','app','8080']);
  assert.match(previewAddress,/^127\.0\.0\.1:\d+$/);
  const previewBase=`http://${previewAddress}`;
  const probe=await native(previewBase,'auth/login','',{username:'native_tester',password:'native-test-password',device_id:randomUUID(),device_name:'Preview API readiness'});
  assert.equal(probe.status,200);
  const probeTokens=await probe.json();
  // The VM and Mac are independent clocks. Check the exact advertised lifetime
  // and use the server's own HTTP Date (one-second precision) for expiry bounds.
  assert.equal(probeTokens.expires_in,30,'Preview must advertise its configured short access lifetime');
  const expiresAfterServerDate=new Date(probeTokens.access_expires_at).valueOf()-Date.parse(probe.headers.get('date'));
  assert.ok(expiresAfterServerDate>20000&&expiresAfterServerDate<=31000,'Preview expiry must agree with its server clock');
  fs.writeFileSync(path.join(evidence,'preview-auth-check.json'),JSON.stringify({expires_in:probeTokens.expires_in,access_expires_at:probeTokens.access_expires_at,server_date:probe.headers.get('date'),client_time:new Date().toISOString()},null,2)+'\n',{mode:0o600});
  assert.equal((await native(previewBase,'titles/mapped',probeTokens.access_token)).status,200);
  assert.equal((await native(previewBase,'auth/logout',probeTokens.access_token,{})).status,200);
  const cleanupArgs=[...compose,'down','--timeout','10'];
  const state={project,evidence,server:previewBase,username:'native_tester',password:'native-test-password',media:'Native generated fixture',duration_seconds:90,access_lifetime_seconds:30,cleanup:{executable:'docker',arguments:cleanupArgs},verified:'API fixture only; native app observations remain owner-performed'};
  fs.writeFileSync(path.join(evidence,'preview.json'),JSON.stringify(state,null,2)+'\n',{mode:0o600});
  fs.writeFileSync(path.join(evidence,'stop-preview.mjs'),`import {spawnSync} from 'node:child_process';\nconst result=spawnSync('docker',${JSON.stringify(cleanupArgs)},{stdio:'inherit'});\nprocess.exit(result.status??1);\n`,{mode:0o600});
  previewReady=true;
  console.log(`Owner preview ready: ${previewBase}; sign in as native_tester / native-test-password.`);
  console.log(`Stop only this preview: devbox run -- node ${path.join(evidence,'stop-preview.mjs')}`);
 }
}finally{
 try{
  const logs=await stack(['logs','--no-color']);
  fs.writeFileSync(path.join(evidence,'stack.log'),logs,{mode:0o600});
 }finally{
  if(!previewReady)await stack(['down','--timeout','10']);
  console.log(`Fixture files and logs retained: ${evidence}`);
 }
}
