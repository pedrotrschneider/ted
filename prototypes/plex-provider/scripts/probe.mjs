import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';

const root = fileURLToPath(new URL('../', import.meta.url));
const output = path.join(root, 'evidence');
fs.mkdirSync(output, {recursive: true});
const prefsPath = path.join(root, 'runtime/plex-config/Library/Application Support/Plex Media Server/Preferences.xml');
// Use only the token belonging to this isolated server, never TED's integration credentials.
const prefs = fs.readFileSync(prefsPath, 'utf8');
const token = prefs.match(/\bPlexOnlineToken="([^"]+)"/)?.[1];
const plex = 'http://127.0.0.1:32490';
const provider = 'http://127.0.0.1:42390';
const headers = {'Accept': 'application/json', 'X-Plex-Client-Identifier': 'ted-plex-prototype-check', 'X-Plex-Product': 'TED Plex Prototype CLI', 'X-Plex-Pms-Api-Version': '1.2.3'};
if (token) headers['X-Plex-Token'] = token;
const results = [];
function redact(value) {
  if (typeof value === 'string') return token ? value.replaceAll(token, '[redacted]') : value;
  if (Array.isArray(value)) return value.map(redact);
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key,v]) => [key, /token|username|email/i.test(key) ? '[redacted]' : redact(v)]));
  return value;
}
async function call(name, endpoint, options = {}) {
  const started = Date.now();
  let response;
  try {
    response = await fetch(endpoint, {...options, headers: {...headers, ...options.headers}, signal: AbortSignal.timeout(45000)});
  } catch (error) {
    const result = {name, status:null, ms:Date.now()-started, error:error.message, cause:error.cause?.message};
    fs.writeFileSync(path.join(output,name+'.json'),JSON.stringify(redact(result),null,2)+'\n');
    results.push(result); console.log(name,'HTTP client failure:',result.cause ?? result.error);
    return {response:null,data:{}};
  }
  let body, data;
  if (response.headers.get('content-type')?.startsWith('video/')) {
    const bytes = Buffer.from(await response.arrayBuffer());
    data = {bytes:bytes.length,sha256:createHash('sha256').update(bytes).digest('hex')};
    body = '';
  } else {
    body = await response.text();
    try { data = JSON.parse(body); } catch { data = body; }
  }
  const result = redact({name, status: response.status, ms: Date.now() - started, headers: {'content-type':response.headers.get('content-type'), 'content-length':response.headers.get('content-length'), 'content-range':response.headers.get('content-range')}, body: data});
  fs.writeFileSync(path.join(output, name + '.json'), JSON.stringify(result, null, 2) + '\n');
  results.push({name, status: result.status, ms: result.ms});
  console.log(name, response.status, typeof data === 'object' ? JSON.stringify({size: data.MediaContainer?.size, providers: data.MediaContainer?.MediaProvider?.map(p=>({id:p.id,identifier:p.identifier})), decision: data.MediaContainer?.mdeDecisionText ?? data.MediaContainer?.generalDecisionText}) : body.slice(0,100));
  return {response, data};
}

const identity = (await call('identity', plex+'/identity')).data.MediaContainer;
console.log(JSON.stringify({version:identity.version, claimed:identity.claimed, ownerTokenPresent:Boolean(token)}));
const url = new URL(plex+'/media/providers'); url.searchParams.set('url','http://provider:42390/provider');
const connectionPath = path.join(root,'runtime/connection.json');
let registered;
if (fs.existsSync(connectionPath)) {
  const saved = JSON.parse(fs.readFileSync(connectionPath));
  if (saved.machineIdentifier !== identity.machineIdentifier) throw new Error('Prototype server identity changed; inspect the existing provider before registering again');
  registered = {id:saved.providerId,identifier:'tv.plex.provider.ted.cinemeta'};
} else {
  registered = (await call('registration',url,{method:'POST'})).data.MediaContainer?.MediaProvider?.[0];
}
if (!registered?.id) throw new Error('Plex did not register the provider');
const id = registered.id;
fs.writeFileSync(connectionPath,JSON.stringify({machineIdentifier:identity.machineIdentifier,providerId:id},null,2)+'\n');
const proxy = `${plex}/media/providers/${id}/proxy`;
const publicList = await call('discovery-owner',plex+'/media/providers');
await call('discovery-owner-included',plex+'/media/providers?includeProviders=1&includeSubscriptions=1');
await call('native-sections',plex+'/library/sections');
await call('provider-sections',proxy+'/library/sections');
await call('movies-proxied',proxy+'/library/sections/movies/all');
await call('shows-proxied',proxy+'/library/sections/shows/all');
await call('search-proxied',proxy+'/hubs/search?query=Batman');
const fixture = (await call('fixture-proxied',proxy+'/library/metadata/demo-movie')).data.MediaContainer?.Metadata?.[0];
await call('episode-proxied',proxy+'/library/metadata/demo-episode');
await call('series-children-proxied',proxy+'/library/metadata/demo-show/children');
await call('related-proxied',proxy+'/library/metadata/demo-movie/related');
await call('provider-playqueue',proxy+'/playQueues?type=video&key=/library/metadata/demo-movie',{method:'POST'});
const uri = `server://${identity.machineIdentifier}/${registered.identifier}/library/metadata/demo-movie`;
await call('native-playqueue',plex+'/playQueues?type=video&uri='+encodeURIComponent(uri),{method:'POST'});
// Reproduce the URI Plex Web actually used during the owner's Play check.
const clientURI = `server://${identity.machineIdentifier}/com.plexapp.plugins.library/media/providers/${id}/proxy/library/metadata/demo-movie`;
await call('native-client-playqueue',plex+'/playQueues?type=video&uri='+encodeURIComponent(clientURI)+'&repeat=0&own=1&includeExternalMedia=1',{method:'POST'});
// Fetch source using the actual key returned by Plex, without inventing a local library item.
const part = fixture?.Media?.[0]?.Part?.[0];
if (!part?.key) throw new Error('No generated fixture source');
await call('stream-range',new URL(part.key,plex),{headers:{Range:'bytes=1000-1999'}});
const decision = new URL(plex+'/video/:/transcode/universal/decision');
for (const [key,v] of Object.entries({path:`/media/providers/${id}/proxy/library/metadata/demo-movie`,mediaIndex:'0',partIndex:'0',protocol:'http',directPlay:'1',directStream:'1',videoQuality:'100',videoResolution:'640x360',maxVideoBitrate:'4000',audioBoost:'100',session:'ted-prototype-playback',hasMDE:'1'})) decision.searchParams.set(key,v);
await call('native-playback-decision',decision,{headers:{'X-Plex-Client-Profile-Name':'Chrome','X-Plex-Platform':'Chrome','X-Plex-Platform-Version':'130','X-Plex-Device':'OSX','X-Plex-Device-Name':'Prototype CLI'}});
await call('timeline-write',proxy+'/:/timeline?ratingKey=demo-movie&time=21000&state=paused',{method:'GET'});
await call('resume-read',proxy+'/library/metadata/demo-movie');
await call('continue-watching',proxy+'/hubs/continueWatching');
await call('watch-state',provider+'/prototype/state');
const discovered = publicList.data.MediaContainer?.MediaProvider?.some(p=>p.identifier===registered.identifier) ?? false;
const summary = {time:new Date().toISOString(),version:identity.version,claimed:identity.claimed,ownerTokenPresent:Boolean(token),providerId:id,identifier:registered.identifier,discovered,results};
fs.writeFileSync(path.join(output,'summary.json'),JSON.stringify(summary,null,2)+'\n');
fs.writeFileSync(path.join(root,'runtime/provider/connection.json'),JSON.stringify({machineIdentifier:identity.machineIdentifier,providerId:id},null,2)+'\n');
console.log(JSON.stringify({providerId:id,discovered,detailsURL:`${plex}/web/index.html#!/server/${identity.machineIdentifier}/details?key=${encodeURIComponent('/media/providers/'+id+'/proxy/library/metadata/demo-movie')}`}));
