import type {BrowseSelection,DiscoveryFilters,WatchFilterAccount} from './discovery-filters.ts';
import { ApiError, request } from './api.ts';
import { catalogApi, movie } from './catalog-api.ts';
import type { Movie } from './catalog-api.ts';
import { seriesApi, title } from './series-api.ts';
import type { SeriesTitle } from './series-api.ts';
export type MediaTitle = Movie | SeriesTitle;
export type MediaKind = MediaTitle['type'];
export interface BrowsePage { titles:MediaTitle[]; skip:number; next?:number }
export interface SavedPage { titles:MediaTitle[]; next?:string }
export interface LibraryPage extends SavedPage {watchAccounts:WatchFilterAccount[]}
export const mediaBrowserApi = {
 async browse(kind:MediaKind,search:string,skip:number,signal:AbortSignal,filters:DiscoveryFilters={}):Promise<BrowsePage> {
  if(kind==='movie'){const p=await catalogApi.browse(search,skip,signal,filters);return {titles:p.movies,skip:p.skip,next:p.next_skip}}
  const p=await seriesApi.browse(search,skip,signal,filters);return {titles:p.series,skip:p.skip,next:p.next_skip};
 },
 async library(kind:MediaKind,selection:BrowseSelection,after:string,signal:AbortSignal):Promise<LibraryPage>{
  const q=new URLSearchParams({kind,saved:String(selection.saved),watched:String(selection.watched),search:selection.search,genre:selection.genre,after});
  if(selection.watched&&selection.watchedAccounts!==undefined)q.set('watch_accounts',selection.watchedAccounts.join(','));
  const v=await request(`/api/library-titles?${q}`,'GET',undefined,signal);
  if(!v||typeof v!=='object'||!('titles' in v)||!Array.isArray(v.titles))throw new ApiError('Incomplete saved or watched title list. Refresh to try again.');
  const titles=v.titles.map(v=>kind==='movie'?movie(v):title(v));const next='next_after' in v?v.next_after:undefined;
  if(new Set(titles.map(t=>t.id)).size!==titles.length||titles.some(t=>t.id===after)||(next!==undefined&&(!titles.length||next!==titles.at(-1)?.id)))throw new ApiError('Incomplete saved or watched title list. Refresh to try again.');
  const accounts='watch_accounts' in v?v.watch_accounts:undefined;
  if(!Array.isArray(accounts)||!accounts.length||accounts.some(a=>!a||!Number.isSafeInteger(a.id)||a.id<=0||typeof a.name!=='string'||!/^#[0-9a-f]{6}$/i.test(a.color))||new Set(accounts.map(a=>a.id)).size!==accounts.length)throw new ApiError('Watched account filters are unavailable. Refresh to try again.');
  return {titles,next:next as string|undefined,watchAccounts:accounts};
 },
 async saved(kind:MediaKind,after:string,signal:AbortSignal):Promise<SavedPage> {
  if(kind==='movie'){const p=await catalogApi.saved(after,signal);return {titles:p.movies,next:p.next_after}}
  return {titles:await seriesApi.saved(signal)};
 }
};
export function appendBrowse(old:BrowsePage|null,next:BrowsePage):BrowsePage {
 if(next.skip===0)return next;
 if(!old||old.next!==next.skip||next.titles.some(t=>old.titles.some(p=>p.id===t.id)))throw new ApiError('Catalog results changed while loading. Refresh to start again.');
 return {...next,titles:[...old.titles,...next.titles]};
}
