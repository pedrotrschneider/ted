import {request,ApiError} from './api.ts';
import {movie} from './catalog-api.ts';
import {title as seriesTitle} from './series-api.ts';
import {validRating} from './personal-rating.ts';
import type {MediaTitle} from './media-browser-api.ts';
export interface TitlePreferences {watchlist:boolean;user_rating:number|null}
export function preferences(raw:unknown):TitlePreferences {
 const v=raw as TitlePreferences;
 if(!v||typeof v.watchlist!=='boolean'||!validRating(v.user_rating))throw new ApiError('TED returned incomplete title preferences. Refresh to check the change.');
 return v;
}
export async function setTitlePreference(title:MediaTitle,field:'watchlist'|'rating',value:boolean|number|null){
 return preferences(await request(`/api/titles/${title.type}/${title.id}/preferences/${field}`,'PUT',field==='watchlist'?{watchlist:value}:{user_rating:value}));
}
export async function collection(list:string,kind:string,after:string,signal:AbortSignal){
 const q=new URLSearchParams({kind,after});
 const raw=await request(`/api/collections/${list}?${q}`,'GET',undefined,signal) as {titles?:unknown[];next_after?:string};
 if(!raw||!Array.isArray(raw.titles)||raw.titles.length>50||typeof raw.next_after!=='string'||raw.next_after.length>512)throw new ApiError('Invalid personal title list.');
 const titles=raw.titles.map(value=>(value as {type?:string})?.type==='movie'?movie(value):seriesTitle(value));
 if(new Set(titles.map(t=>`${t.type}/${t.id}`)).size!==titles.length||titles.some(t=>kind&&t.type!==kind)||raw.next_after&&!titles.length)throw new ApiError('Incomplete personal title list. Previous results are retained.');
 return {titles,next:raw.next_after};
}
