import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';

const root = fileURLToPath(new URL('../', import.meta.url));
const prefsPath = path.join(root,'runtime/plex-config/Library/Application Support/Plex Media Server/Preferences.xml');
const prefs = fs.existsSync(prefsPath) ? fs.readFileSync(prefsPath,'utf8') : '';
const secrets = [...prefs.matchAll(/\b(?:PlexOnlineToken|PlexOnlineUsername|PlexOnlineMail|PlexOnlineHome)="([^"]+)"/g)].map(m=>m[1]).filter(s=>s.length>3);
function clean(value) {
  if (typeof value === 'string') {
    for (const secret of secrets) value = value.replaceAll(secret,'[redacted]');
    return value.replace(/(X-Plex-Token[=:])[\w.-]+/gi,'$1[redacted]');
  }
  if (Array.isArray(value)) return value.map(clean);
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([k,v])=>[k,/token|username|email|myPlexUsername/i.test(k) && !/Present$/.test(k) ? '[redacted]' : clean(v)]));
  return value;
}
const dir = path.join(root,'evidence');
for (const name of fs.readdirSync(dir)) {
  const file = path.join(dir,name);
  const text = fs.readFileSync(file,'utf8');
  if (name.endsWith('.json')) {
    try {fs.writeFileSync(file,JSON.stringify(clean(JSON.parse(text)),null,2)+'\n');continue;} catch {}
  }
  fs.writeFileSync(file,clean(text));
}
const sources = ['main.go','main_test.go','go.mod','Dockerfile','compose.yaml','index.html',...fs.readdirSync(path.join(root,'scripts')).map(n=>'scripts/'+n)];
const hashes = Object.fromEntries(sources.map(name=>[name,createHash('sha256').update(fs.readFileSync(path.join(root,name))).digest('hex')]));
fs.writeFileSync(path.join(dir,'tested-sources.json'),JSON.stringify({time:new Date().toISOString(),sha256:hashes},null,2)+'\n');
console.log('Sanitized saved evidence and recorded source hashes.');
