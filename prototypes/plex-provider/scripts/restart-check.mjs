import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';

const root = fileURLToPath(new URL('../', import.meta.url));
const prefs = fs.readFileSync(path.join(root,'runtime/plex-config/Library/Application Support/Plex Media Server/Preferences.xml'),'utf8');
const token = prefs.match(/\bPlexOnlineToken="([^"]+)"/)?.[1];
const {providerId} = JSON.parse(fs.readFileSync(path.join(root,'runtime/connection.json')));
const headers = {Accept:'application/json'};
if (token) headers['X-Plex-Token'] = token;
async function snapshot() {
  const get = async url => {
    const r = await fetch(url,{headers,signal:AbortSignal.timeout(5000)});
    if (!r.ok) throw new Error('Snapshot HTTP '+r.status);
    return r.json();
  };
  const identity = (await get('http://127.0.0.1:32490/identity')).MediaContainer;
  const fixture = (await get(`http://127.0.0.1:32490/media/providers/${providerId}/proxy/library/metadata/demo-movie`)).MediaContainer.Metadata[0];
  const state = await get('http://127.0.0.1:42390/prototype/state');
  return {machineIdentifier:identity.machineIdentifier,version:identity.version,providerId,guid:fixture.guid,viewOffset:fixture.viewOffset,viewCount:fixture.viewCount,state};
}
const before = await snapshot();
execFileSync('docker',['compose','-f',path.join(root,'compose.yaml'),'restart','plex','provider'],{stdio:'inherit'});
// A bounded readiness wait is needed only because these services have just restarted.
const deadline = Date.now()+30000;
while (true) {
  try {
    const r = await fetch('http://127.0.0.1:32490/identity',{headers,signal:AbortSignal.timeout(2000)});
    if (r.ok) break;
  } catch {}
  if (Date.now()>deadline) throw new Error('Plex did not become ready after restart');
  await new Promise(resolve=>setTimeout(resolve,500));
}
const after = await snapshot();
const preserved = JSON.stringify(before) === JSON.stringify(after);
fs.writeFileSync(path.join(root,'evidence/restart.json'),JSON.stringify({time:new Date().toISOString(),preserved,before,after},null,2)+'\n');
if (!preserved) throw new Error('Provider identity or fixture state changed across restart');
const database = 'file:/config/Library/Application Support/Plex Media Server/Plug-in Support/Databases/com.plexapp.plugins.library.db?mode=ro';
const sql = 'SELECT (SELECT count(*) FROM metadata_items) AS metadata_items, (SELECT count(*) FROM library_sections) AS library_sections, (SELECT count(*) FROM media_provider_resources) AS media_provider_resources';
const counts = JSON.parse(execFileSync('docker',['compose','-f',path.join(root,'compose.yaml'),'exec','-T','plex','/usr/lib/plexmediaserver/Plex SQLite',database,'-json',sql],{encoding:'utf8'}));
fs.writeFileSync(path.join(root,'evidence/database-counts.json'),JSON.stringify({time:new Date().toISOString(),counts},null,2)+'\n');
console.log(JSON.stringify({restartPreserved:preserved,counts}));
