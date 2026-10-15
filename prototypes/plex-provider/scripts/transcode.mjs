import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';

const root = fileURLToPath(new URL('../', import.meta.url));
const prefs = fs.readFileSync(path.join(root, 'runtime/plex-config/Library/Application Support/Plex Media Server/Preferences.xml'), 'utf8');
const token = prefs.match(/\bPlexOnlineToken="([^"]+)"/)?.[1];
const {providerId} = JSON.parse(fs.readFileSync(path.join(root, 'runtime/connection.json')));
const plex = 'http://127.0.0.1:32490';
const session = 'ted-prototype-hls-check';
const headers = {'Accept':'*/*', 'X-Plex-Client-Identifier':session, 'X-Plex-Product':'TED Plex Prototype CLI', 'X-Plex-Client-Profile-Name':'Chrome', 'X-Plex-Platform':'Chrome', 'X-Plex-Platform-Version':'130', 'X-Plex-Device':'OSX'};
if (token) headers['X-Plex-Token'] = token;
const output = {time:new Date().toISOString(), session, results:[]};
function clean(s) {return token ? s.replaceAll(token, '[redacted]') : s;}
async function request(label, url) {
  const response = await fetch(url, {headers, signal:AbortSignal.timeout(45000)});
  const bytes = Buffer.from(await response.arrayBuffer());
  const text = bytes.toString('utf8');
  const isPlaylist = text.startsWith('#EXTM3U');
  const result = {label, status:response.status, contentType:response.headers.get('content-type'), bytes:bytes.length, sha256:createHash('sha256').update(bytes).digest('hex')};
  if (isPlaylist || !response.ok) result.body = clean(text);
  output.results.push(result);
  console.log(label, response.status, result.contentType, bytes.length);
  return {response, bytes, text, isPlaylist, url};
}
try {
  const url = new URL(plex+'/video/:/transcode/universal/start.m3u8');
  for (const [k,v] of Object.entries({path:`/media/providers/${providerId}/proxy/library/metadata/demo-movie`,mediaIndex:'0',partIndex:'0',protocol:'hls',directPlay:'0',directStream:'0',videoQuality:'100',videoResolution:'640x360',maxVideoBitrate:'1000',audioBoost:'100',offset:'0',session,hasMDE:'1',fastSeek:'1'})) url.searchParams.set(k,v);
  let playlist = await request('start',url);
  if (playlist.response.ok && playlist.isPlaylist) {
    if (playlist.text.includes('#EXT-X-STREAM-INF')) {
      const variant = playlist.text.split(/\r?\n/).find(line=>line && !line.startsWith('#'));
      playlist = await request('variant',new URL(variant,playlist.url));
    }
    const segments = playlist.text.split(/\r?\n/).filter(line=>line && !line.startsWith('#'));
    const dir = path.join(root,'runtime/hls-check');
    fs.mkdirSync(dir,{recursive:true});
    for (let i=0;i<Math.min(3,segments.length);i++) {
      const segment = await request('segment-'+i,new URL(segments[i],playlist.url));
      if (!segment.response.ok) break;
      fs.writeFileSync(path.join(dir,`segment-${i}.ts`),segment.bytes);
    }
  }
} catch (error) {
  output.error = clean(error.message);
  output.cause = clean(error.cause?.message ?? '');
  console.log('Transcode request failed:',output.cause || output.error);
} finally {
  try {
    const stop = new URL(plex+'/video/:/transcode/universal/stop');
    stop.searchParams.set('session',session);
    await request('stop',stop);
  } catch (error) {output.stopError = clean(error.message);}
  fs.writeFileSync(path.join(root,'evidence/transcode.json'),JSON.stringify(output,null,2)+'\n');
}
