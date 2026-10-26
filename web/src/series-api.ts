import {discoveryQuery} from './discovery-filters.ts';
import type {DiscoveryFilters} from './discovery-filters.ts';
import { ApiError, request } from './api.ts';
import {titleArtworkURL} from './artwork-url.ts';
import type { Movie } from './catalog-api.ts';
export interface SeriesTitle extends Omit<Movie,'type'> { type:'series' }
export interface Episode { id:string; name:string; season:number; number:number; release_date?:string }
export interface Series extends SeriesTitle { episodes:Episode[];metadata_pending?:boolean }
export interface SeriesPage { series:SeriesTitle[]; skip:number; next_skip?:number }
export interface EpisodeMapping { source_name:string; episode_id:string; media_id:string; torrent_id:string; hash:string; file_id:number; path:string; bytes:number }
export interface SeriesMappings { category:''|'shows'|'anime'; mappings:EpisodeMapping[] }
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object';
const invalid=()=>new ApiError('Incomplete series information. Refresh before changing mappings.');
export function title(v:unknown):SeriesTitle {
 if(!object(v)||typeof v.id!=='string'||!/^tt[0-9]{7,12}$/.test(v.id)||v.type!=='series'||typeof v.name!=='string'||!v.name||typeof v.year!=='string'||typeof v.description!=='string'||typeof v.poster!=='string'||!Array.isArray(v.genres)||!v.genres.every(g=>typeof g==='string'))throw invalid();
 if(v.poster&&!titleArtworkURL(v.poster))throw invalid();
 return v as unknown as SeriesTitle;
}
export const seriesApi={
 async browse(search='',skip=0,signal?:AbortSignal,filters:DiscoveryFilters={}):Promise<SeriesPage>{
  const v=await request(`/api/series?search=${encodeURIComponent(search)}&skip=${skip}${discoveryQuery(filters)}`,'GET',undefined,signal);
  if(!object(v)||!Array.isArray(v.series)||v.skip!==skip)throw invalid();const series=v.series.map(title);
  if(new Set(series.map(s=>s.id)).size!==series.length||(v.next_skip!==undefined&&(search!==''||v.next_skip!==skip+50||!series.length)))throw invalid();
  return {series,skip,next_skip:v.next_skip as number|undefined};
 },
 async saved(signal?:AbortSignal):Promise<SeriesTitle[]>{const v=await request('/api/series/saved','GET',undefined,signal);if(!object(v)||!Array.isArray(v.series))throw invalid();return v.series.map(title)},
 async detail(id:string,signal?:AbortSignal):Promise<Series>{
  const v=await request(`/api/series/${encodeURIComponent(id)}`,'GET',undefined,signal);const s=title(v);
  if(!object(v)||s.id!==id||!Array.isArray(v.episodes))throw invalid();
  const episodes=v.episodes.map(e=>{if(!object(e)||!Number.isSafeInteger(e.season)||Number(e.season)<0||!Number.isSafeInteger(e.number)||Number(e.number)<0||typeof e.id!=='string'||!e.id.startsWith(id+':')||!/^tt[0-9]{7,12}:[0-9]{1,4}:[0-9]{1,5}$/.test(e.id)||typeof e.name!=='string'||(e.release_date!==undefined&&(typeof e.release_date!=='string'||!/^\d{4}-\d{2}-\d{2}$/.test(e.release_date))))throw invalid();return e as unknown as Episode});
  if(new Set(episodes.map(e=>e.id)).size!==episodes.length)throw invalid();return {...s,episodes};
 },
 async mappings(id:string,signal?:AbortSignal):Promise<SeriesMappings>{
  const v=await request(`/api/admin/series/${encodeURIComponent(id)}/mappings`,'GET',undefined,signal);
  if(!object(v)||!['','shows','anime'].includes(String(v.category))||!Array.isArray(v.mappings))throw invalid();
  const mappings=v.mappings.map(m=>{if(!object(m)||typeof m.source_name!=='string'||!m.source_name||typeof m.episode_id!=='string'||!m.episode_id.startsWith(id+':')||typeof m.media_id!=='string'||typeof m.path!=='string'||typeof m.torrent_id!=='string'||typeof m.hash!=='string'||!Number.isSafeInteger(m.file_id)||!Number.isSafeInteger(m.bytes))throw invalid();return m as unknown as EpisodeMapping});
  if(new Set(mappings.map(m=>m.episode_id)).size!==mappings.length)throw invalid();return {category:v.category as SeriesMappings['category'],mappings};
 },

};
export function appendSeries(old:SeriesPage|null,next:SeriesPage):SeriesPage {
 if(next.skip===0)return next;
 if(!old||old.next_skip!==next.skip||next.series.some(s=>old.series.some(p=>p.id===s.id)))throw new ApiError('Series results changed. Refresh to start again.');
 return {...next,series:[...old.series,...next.series]};
}
