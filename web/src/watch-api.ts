import { ApiError, request } from './api.ts';
export interface WatchState {played:boolean;position_ticks:number;play_count:number;last_played:string|null}
export interface WatchEntry {id:number;media_id:string;title:string;kind:string;state:WatchState;observed_at:string}
export interface WatchCurrent {revision:number;media_id:string;title:string;state:WatchState;source:string;needs_review:boolean;observed_at:string}
export interface WatchHistory {entries:WatchEntry[];current:WatchCurrent[];next_before?:number}
const obj=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const integer=(v:unknown)=>Number.isSafeInteger(v)&&Number(v)>=0;
const text=(v:unknown)=>typeof v==='string';
const date=(v:unknown)=>typeof v==='string'&&Number.isFinite(Date.parse(v));
const fail=():never=>{throw new ApiError('TED returned incomplete watch data. Previous results are retained.');};
function state(v:unknown):v is WatchState{return obj(v)&&typeof v.played==='boolean'&&integer(v.position_ticks)&&integer(v.play_count)&&(v.last_played===null||date(v.last_played))}
export function parseWatchHistory(v:unknown):WatchHistory {
 if(!obj(v)||!Array.isArray(v.entries)||!Array.isArray(v.current))return fail();
 if(!v.entries.every(e=>obj(e)&&integer(e.id)&&Number(e.id)>0&&text(e.media_id)&&text(e.title)&&text(e.kind)&&state(e.state)&&date(e.observed_at))||!v.current.every(e=>obj(e)&&text(e.media_id)&&text(e.title)&&text(e.source)&&typeof e.needs_review==='boolean'&&integer(e.revision)&&Number(e.revision)>0&&state(e.state)&&date(e.observed_at)))return fail();
 if(v.entries.some((e,i)=>i>0&&e.id>=(v.entries as WatchEntry[])[i-1].id)||v.next_before!==undefined&&(!integer(v.next_before)||v.next_before!==v.entries.at(-1)?.id))return fail();
 return v as unknown as WatchHistory;
}
export const watchAPI={
 edit:async(media:string,revision:number,action:string,dateMode?:'now'|'undated')=>{const v=await request(`/api/watch-history/${encodeURIComponent(media)}`,'POST',{revision,action,date_mode:dateMode});if(!obj(v)||v.status!=='accepted'||!text(v.id))fail();},
 history:async(before?:number,signal?:AbortSignal)=>parseWatchHistory(await request('/api/watch-history'+(before===undefined?'':`?before=${before}`),'GET',undefined,signal)),
};
