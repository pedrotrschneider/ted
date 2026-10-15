// One-time, non-destructive migration from this Compose project's legacy named volume.
// Run through Devbox, after building the new application image.
import {execFileSync} from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
const docker=(...args)=>execFileSync('docker',args,{encoding:'utf8',maxBuffer:16*1024*1024}).trim();
const compose=(...args)=>docker('compose',...args);
const config=JSON.parse(compose('config','--format','json'));
const configured=config.services.db.volumes.find(m=>m.target==='/var/lib/postgresql');
if(configured?.type!=='bind')throw new Error('Configure the database bind mount before migration.');
const target=path.resolve(configured.source);
const db=compose('ps','-q','db'), app=compose('ps','-q','app');
if(!db||!app)throw new Error('The existing application and database must be running.');
const inspection=JSON.parse(docker('inspect',db))[0];
const source=inspection.Mounts.find(m=>m.Destination==='/var/lib/postgresql');
if(source?.Type!=='volume')throw new Error('The current database is not a legacy named-volume deployment; nothing changed.');
if(fs.existsSync(target)&&fs.readdirSync(target).length)throw new Error('Destination contains files; refusing to overwrite it.');
const stamp=new Date().toISOString().replaceAll(':','-');
const backup=path.join(path.dirname(target),'backups','volume-migration-'+stamp);
const staging=target+'-staging-'+stamp;
fs.mkdirSync(backup,{recursive:true,mode:0o700});fs.mkdirSync(staging,{mode:0o700});
if(fs.existsSync('.env')){fs.copyFileSync('.env',path.join(backup,'deployment.env'),fs.constants.COPYFILE_EXCL);fs.chmodSync(path.join(backup,'deployment.env'),0o600);}
fs.writeFileSync(path.join(backup,'legacy-storage.json'),JSON.stringify({volume:source.Name,image:inspection.Image,target},null,2)+'\n',{mode:0o600});
// User/session fingerprints are retained privately; no passwords or session data are printed.
const query=`SELECT json_build_object(
 'users', (SELECT md5(coalesce(string_agg(row_to_json(u)::text,',' ORDER BY id),'')) FROM (SELECT id,username,password_hash,role,disabled,session_version,created_at FROM users) u),
 'sessions',(SELECT md5(coalesce(string_agg(row_to_json(s)::text,',' ORDER BY token),'')) FROM sessions s),
 'limits',(SELECT md5(coalesce(string_agg(row_to_json(l)::text,',' ORDER BY key),'')) FROM login_limits l));`;
const snapshot=()=>compose('exec','-T','db','psql','-U','ted','-d','ted','-Atc',query);
console.log('Stopping TED writes and saving a local database dump.');
compose('stop','app');
const before=snapshot();
fs.writeFileSync(path.join(backup,'before.json'),before+'\n',{mode:0o600});
const fd=fs.openSync(path.join(backup,'ted.dump'),'wx',0o600);
try{execFileSync('docker',['compose','exec','-T','db','pg_dump','-U','ted','-d','ted','-Fc'],{stdio:['ignore',fd,'pipe']});}finally{fs.closeSync(fd);}
compose('stop','db');
console.log('Copying the cleanly stopped database to host files; the original volume is retained.');
docker('run','--rm','--network','none','--entrypoint','sh',
 '--mount',`type=volume,src=${source.Name},dst=/var/lib/postgresql,readonly`,
 '--mount',`type=bind,src=${staging},dst=/target`,inspection.Image,
 '-c','cp -a /var/lib/postgresql/. /target/');
if(!fs.existsSync(path.join(staging,'18','docker','PG_VERSION')))throw new Error('Copied database is missing its PostgreSQL 18 version file; original volume retained.');
if(fs.existsSync(target))fs.rmdirSync(target); // Only the already-checked empty destination.
fs.renameSync(staging,target);
compose('up','-d','--wait','db');
const after=snapshot();
if(after!==before)throw new Error('Database verification differs. Application remains stopped; retain both copies for investigation.');
fs.writeFileSync(path.join(backup,'after.json'),after+'\n',{mode:0o600});
compose('up','-d','--wait','app');
console.log(`Migration verified. Database files: ${target}\nBackup and migration evidence: ${backup}\nOriginal volume retained as a rollback copy: ${source.Name}`);
