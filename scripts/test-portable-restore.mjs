// Read-only snapshot of the live TED deployment; all restoration effects are
// confined to new bind-mounted directories and an internal Docker network.
import {loadEnvFile} from 'node:process';
import {execFileSync} from 'node:child_process';
import {mkdirSync,readFileSync,writeFileSync,cpSync,readdirSync,statSync,existsSync,openSync,closeSync} from 'node:fs';
import {resolve,join,relative} from 'node:path';
import {createHash,randomBytes} from 'node:crypto';
import assert from 'node:assert/strict';
loadEnvFile('.env');
const root=resolve('.devbox/test-evidence/portable-'+Date.now()),project='ted-portable-'+Date.now();
mkdirSync(root,{recursive:true,mode:0o700});
const sourceConfig=resolve(process.env.TED_CONFIG_DIR||'Config'),sourceMedia=resolve(process.env.TED_MEDIA_DIR||'Media');
const env={...process.env,TED_DB_PASSWORD:randomBytes(24).toString('hex'),TED_REAL_DEBRID_API_KEY:'',TED_PORT:'0',TED_BIND_ADDRESS:'127.0.0.1',TED_INSECURE_HTTP:'true',TED_UID:String(process.getuid()),TED_GID:String(process.getgid()),TED_CONFIG_DIR:root+'/Config',TED_MEDIA_DIR:root+'/Media'};
const override=root+'/isolated.yaml';writeFileSync(override,'services:\n  app:\n    image: ted-app:latest\nnetworks:\n  default:\n    internal: true\n');
const args=['compose','--env-file','/dev/null','-f',resolve('compose.yaml'),'-f',override,'-p',project];
function docker(args,options={}){try{return execFileSync('docker',args,{env,encoding:'utf8',maxBuffer:64<<20,stdio:['pipe','pipe','pipe'],...options});}catch(e){writeFileSync(root+'/command-error.log',e.stderr??'Docker command failed',{mode:0o600});throw Error('Docker command failed; private evidence retained.');}}
const compose=(...a)=>docker([...args,...a]);
const sql=(q,live=false)=>docker([...(live?['compose']:args),'exec','-T','db','psql','-U','ted','-d','ted','-At','-v','ON_ERROR_STOP=1'],{input:q,env:live?process.env:env}).trim();
const selected=sql("SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename NOT IN ('sessions','login_limits','river_client','river_client_queue','river_leader') ORDER BY tablename;",true).split('\n');
function fingerprint(live=false){const result={};for(const t of selected){assert.match(t,/^[a-z_0-9]+$/);result[t]=JSON.parse(sql(`SELECT json_build_object('count',count(*),'digest',md5(COALESCE(string_agg(row_to_json(t)::text,E'\\n' ORDER BY row_to_json(t)::text),''))) FROM "${t}" t;`,live));}return result;}
function files(dir){const result={};function walk(path){for(const name of readdirSync(path)){const p=join(path,name),s=statSync(p);if(s.isDirectory())walk(p);else if(s.isFile())result[relative(dir,p)]=createHash('sha256').update(readFileSync(p)).digest('hex');}}walk(dir);return result;}
async function ready(){
 // The internal network deliberately has no route through Colima's host-port
 // forwarder. Probe from the app container, as its own healthcheck does.
 for(let n=0;n<60;n++){
  try{execFileSync('docker',[...args,'exec','-T','app','ted','healthcheck'],{env,stdio:['ignore','pipe','pipe']});return;}
  catch{await new Promise(r=>setTimeout(r,500));}
 }
 throw Error('Restored app did not become ready');
}

let before,manifest;
try{
 assert.equal(sql("SELECT (SELECT count(*) FROM mapping_operations WHERE phase IN ('queued','validating','publishing'))+(SELECT count(*) FROM local_files WHERE phase IN ('queued','downloading','validating','publishing'))+(SELECT count(*) FROM library_removals WHERE phase!='complete');",true),'0','Snapshot requires idle media operations');
 before=fingerprint(true);manifest={assets:Object.fromEntries(['metadata','recovery'].filter(name=>existsSync(sourceConfig+'/'+name)).map(name=>[name,files(sourceConfig+'/'+name)])),media:files(sourceMedia)};
 mkdirSync(env.TED_CONFIG_DIR+'/postgres',{recursive:true});for(const name of Object.keys(manifest.assets))cpSync(sourceConfig+'/'+name,env.TED_CONFIG_DIR+'/'+name,{recursive:true});cpSync(sourceMedia,env.TED_MEDIA_DIR,{recursive:true});cpSync('.env',root+'/original-deployment.env');
 const fd=openSync(root+'/ted.dump','wx',0o600);try{docker(['compose','exec','-T','db','pg_dump','-U','ted','-d','ted','-Fc'],{env:process.env,stdio:['ignore',fd,'pipe']});}finally{closeSync(fd);}
 assert.deepEqual(fingerprint(true),before,'Live durable data changed during snapshot');for(const [name,digest] of Object.entries(manifest.assets))assert.deepEqual(files(sourceConfig+'/'+name),digest,'Live asset files changed during copy');assert.deepEqual(files(sourceMedia),manifest.media,'Live media changed during copy');
 compose('up','-d','--wait','db');docker([...args,'exec','-T','db','pg_restore','--exit-on-error','--no-owner','--no-acl','-U','ted','-d','ted'],{input:readFileSync(root+'/ted.dump')});assert.deepEqual(fingerprint(),before,'Logical restore differs');
 compose('up','-d','--no-build','app');await ready();assert.deepEqual(fingerprint(),before,'App startup changed restored durable data');
 compose('restart','app');await ready();compose('restart','db');await ready();assert.deepEqual(fingerprint(),before,'Restarts changed restored durable data');
 // A new PostgreSQL cluster has been verified from the logical dump. Now test
 // the alternate stopped-file-copy path at another host location.
 compose('down');const relocated=root+'/relocated';mkdirSync(relocated,{recursive:true});
 docker(['run','--rm','--network','none','--mount',`type=bind,src=${root}/Config,dst=/source,readonly`,'--mount',`type=bind,src=${relocated},dst=/var/lib/postgresql`,'--entrypoint','sh','postgres:18','-c','cp -a /source/. /var/lib/postgresql/']);
 cpSync(root+'/Media',relocated+'/Media',{recursive:true});env.TED_CONFIG_DIR=relocated;env.TED_MEDIA_DIR=relocated+'/Media';compose('up','-d','--no-build','--wait');await ready();assert.deepEqual(fingerprint(),before,'Relocated stopped copy differs');
 for(const [name,digest] of Object.entries(manifest.assets))assert.deepEqual(files(relocated+'/'+name),digest);assert.deepEqual(files(relocated+'/Media'),manifest.media);
 const mounts=JSON.parse(docker(['inspect',compose('ps','-q','db').trim()]))[0].Mounts;assert.ok(mounts.every(m=>m.Type==='bind'));
 const evidence={at:new Date().toISOString(),isolated:true,logicalRestore:true,stoppedCopyRelocation:true,appAndDatabaseRestarts:true,durableTables:selected.length,counts:Object.fromEntries(Object.entries(before).map(([k,v])=>[k,v.count])),assetFiles:Object.values(manifest.assets).reduce((n,files)=>n+Object.keys(files).length,0),mediaFiles:Object.keys(manifest.media).length,allFileHashesPreserved:true,externalIntegrationsDisabled:true};writeFileSync(root+'/evidence.json',JSON.stringify(evidence,null,2));console.log(JSON.stringify({evidence:root,...evidence}));
}finally{try{writeFileSync(root+'/compose.log',compose('logs','--no-color'),{mode:0o600});}catch{}compose('down');}
