import type { EpisodeMapping } from './series-api.ts';
import type { TorrentDetail, TorrentFile } from './rd-api.ts';
import type { EpisodeAssignments } from './episode-matching.ts';
export interface DraftFile { source:TorrentDetail; file:TorrentFile }
export type EpisodeDraft = Record<string,DraftFile>;
export function sourceAssignments(draft:EpisodeDraft,source:TorrentDetail):EpisodeAssignments {
 return Object.fromEntries(Object.entries(draft).filter(([,v])=>v.source.id===source.id&&v.source.hash===source.hash).map(([id,v])=>[id,v.file.id]));
}
// Update this source's selections, preserving other sources unless explicitly reassigned.
export function mergeSourceAssignments(draft:EpisodeDraft,source:TorrentDetail,assignments:EpisodeAssignments):EpisodeDraft {
 const next={...draft};
 for(const id of Object.keys(sourceAssignments(draft,source)))if(!Object.hasOwn(assignments,id))delete next[id];
 for(const [id,fileID] of Object.entries(assignments)){
  const file=source.files.find(f=>f.id===fileID);
  if(!file||!file.video||file.bytes<=0)throw Error('A selected file is no longer available. Refresh and review the source.');
  // Preserve the original observed identity until the user selects a replacement.
  const old=draft[id];
  next[id]=old&&old.source.id===source.id&&old.source.hash===source.hash&&old.file.id===fileID?old:{source,file};
 }
 return next;
}
export function draftBatches(draft:EpisodeDraft):Array<{source:TorrentDetail;assignments:EpisodeAssignments}> {
 const groups=new Map<string,{source:TorrentDetail;assignments:EpisodeAssignments}>();
 for(const [id,{source,file}] of Object.entries(draft)){
  const key=source.id+':'+source.hash;
  let group=groups.get(key);
  if(!group){group={source:{...source,files:[]},assignments:{}};groups.set(key,group)}
  const previous=group.source.files.find(f=>f.id===file.id);
  if(previous&&(previous.path!==file.path||previous.bytes!==file.bytes))throw Error('Source file information changed. Clear and review its assignments before saving.');
  if(!previous)group.source.files.push(file);
  group.assignments[id]=file.id;
 }
 return [...groups.values()];
}

export function seasonFileIDs(draft:EpisodeDraft,source:TorrentDetail,episodeIDs:string[],saved:Array<{episode_id:string;torrent_id:string;hash:string;file_id:number}>):number[] {
 return episodeIDs.flatMap(id=>{
  const choice=draft[id];
  if(choice)return choice.source.id===source.id&&choice.source.hash===source.hash?[choice.file.id]:[];
  return saved.filter(m=>m.episode_id===id&&m.torrent_id===source.id&&m.hash===source.hash).map(m=>m.file_id);
 });
}


export function draftMappingStatus(choice:DraftFile|undefined,saved:EpisodeMapping|undefined):'unchanged'|'modified'|'unmapped' {
 if(!choice)return saved?'unchanged':'unmapped';
 if(saved&&choice.source.id===saved.torrent_id&&choice.source.hash===saved.hash&&choice.file.id===saved.file_id&&choice.file.path===saved.path&&choice.file.bytes===saved.bytes)return 'unchanged';
 return 'modified';
}

// Continue forward in displayed order, using the effective draft-over-saved mapping.
export function nextUnmatchedFile(files:TorrentFile[],after:number,source:TorrentDetail,draft:EpisodeDraft,saved:Array<{episode_id:string;torrent_id:string;hash:string;file_id:number}>):number|undefined {
 const end=files.findIndex(file=>file.id===after);
 if(end<0)return undefined;
 const episodes=[...new Set([...Object.keys(draft),...saved.map(mapping=>mapping.episode_id)])];
 const assigned=new Set(seasonFileIDs(draft,source,episodes,saved));
 return files.slice(end+1).find(file=>!assigned.has(file.id))?.id;
}
