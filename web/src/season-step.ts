import type { Episode } from './series-api.ts';
import type { TorrentDetail, TorrentFile } from './rd-api.ts';
import type { EpisodeDraft } from './episode-draft.ts';
import { mergeSourceAssignments, sourceAssignments } from './episode-draft.ts';
import { applySeasonRange, orderedEpisodes } from './episode-matching.ts';
export function seasonSteps(episodes:Episode[]):number[]{return [...new Set(orderedEpisodes(episodes).map(e=>e.season))]}
export function assignSeasonStep(draft:EpisodeDraft,source:TorrentDetail,files:TorrentFile[],episodes:Episode[],index:number,start:number,occupied:Set<string>):{draft:EpisodeDraft;index:number}{
 const season=seasonSteps(episodes)[index];if(season===undefined)throw Error('All seasons reviewed. Go back to revise a season.');
 const assignments=applySeasonRange(files,episodes.filter(e=>e.season===season),start,sourceAssignments(draft,source),occupied);
 return {draft:mergeSourceAssignments(draft,source,assignments),index:index+1};
}
// Skipping is navigation only: preserve assignments from every source.
export function skipMappingStep(draft:EpisodeDraft,index:number,total:number):{draft:EpisodeDraft;index:number}{
 return {draft,index:Math.min(index+1,total)};
}
export function skipSeasonStep(draft:EpisodeDraft,episodes:Episode[],index:number):{draft:EpisodeDraft;index:number}{
 return skipMappingStep(draft,index,seasonSteps(episodes).length);
}
