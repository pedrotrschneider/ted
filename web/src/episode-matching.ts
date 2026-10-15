import type { Episode } from './series-api.ts';
import type { TorrentFile } from './rd-api.ts';
export type EpisodeAssignments = Record<string,number>;
export function orderedEpisodes(episodes:Episode[]):Episode[] {
 return [...episodes].sort((a,b)=>(a.season===0?Infinity:a.season)-(b.season===0?Infinity:b.season)||a.number-b.number);
}
const fileOrder=new Intl.Collator('en',{numeric:true,sensitivity:'base'});
export function orderedFiles(files:TorrentFile[],extras=false):TorrentFile[] {
 return files.filter(f=>f.video&&f.bytes>0&&(extras||!f.suggested_exclusion)).sort((a,b)=>fileOrder.compare(a.path,b.path)||a.id-b.id);
}
export function assignEpisode(draft:EpisodeAssignments,id:string,file:number|null):EpisodeAssignments {
 const next={...draft};if(file===null)delete next[id];else next[id]=file;return next;
}
// Patterns are literal strings with numeric placeholders, not user-supplied regex.
export function episodePattern(pattern:string):(episode:Episode)=>string {
 if(!pattern.trim()||pattern.length>256)throw Error('Enter a pattern up to 256 characters.');
 const token= /\{%([se])(?:\.(\d+))?\}/g;
 const parts:Array<string|{key:'s'|'e';width:number}>=[];let end=0,hasEpisode=false;
 for(const match of pattern.matchAll(token)) {
  parts.push(pattern.slice(end,match.index));const width=Number(match[2]??1);
  if(width<1||width>100)throw Error('Pattern digit width must be between 1 and 100.');
  parts.push({key:match[1] as 's'|'e',width});hasEpisode ||= match[1]==='e';end=match.index+match[0].length;
 }
 parts.push(pattern.slice(end));
 if(parts.some(p=>typeof p==='string'&&p.includes('{%'))||!hasEpisode)throw Error('Use {%s} for season and {%e} for episode, with optional padding such as {%e.3}.');
 return episode=>parts.map(p=>typeof p==='string'?p:String(p.key==='s'?episode.season:episode.number).padStart(p.width,'0')).join('');
}
export function matchPattern(files:TorrentFile[],episodes:Episode[],pattern:string,draft:EpisodeAssignments,occupied:Set<string>):{assignments:EpisodeAssignments;added:number;ambiguous:number} {
 const render=episodePattern(pattern),assignments={...draft};let added=0,ambiguous=0;
 for(const episode of orderedEpisodes(episodes)) {
  if(occupied.has(episode.id)||Object.hasOwn(assignments,episode.id))continue;
  const needle=render(episode);const candidates=files.filter(f=>(f.path.split('/').at(-1)??'').includes(needle));
  if(candidates.length===1){assignments[episode.id]=candidates[0].id;added++}else if(candidates.length>1)ambiguous++;
 }
 return {assignments,added,ambiguous};
}
export function seasonRange(files:TorrentFile[],episodes:Episode[],start:number):TorrentFile[] {
 const a=files.findIndex(f=>f.id===start);
 if(a<0)throw Error('Select the first file of the range.');
 const range=files.slice(a,a+episodes.length);
 if(!episodes.length||range.length!==episodes.length)throw Error(`This season needs ${episodes.length} video files; only ${range.length} remain from this starting file. Choose an earlier file.`);
 return range;
}
export function applySeasonRange(files:TorrentFile[],episodes:Episode[],start:number,draft:EpisodeAssignments,occupied:Set<string>):EpisodeAssignments {
 const order=orderedEpisodes(episodes),range=seasonRange(files,order,start),next={...draft};
 order.forEach((episode,i)=>{if(!occupied.has(episode.id))next[episode.id]=range[i].id});return next;
}
