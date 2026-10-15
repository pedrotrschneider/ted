import {ApiError,request} from './api.ts';
export type WatchDateMode = 'now' | 'undated';
export interface TitleWatchItem {id:string;played:boolean;available:boolean;writable:boolean;pending:boolean;error:string;revision:number}
export interface TitleWatch {items:TitleWatchItem[];version:string}
export function watchScope(value:TitleWatch|undefined,episode?:string,season?:number){
 const items=value?.items.filter(i=>episode?i.id===episode:season===undefined||Number(i.id.split(':')[1])===season)??[];
 const available=items;
 return {played:episode?items.length>0&&items.every(i=>i.played):available.length>0&&available.every(i=>i.played),pending:items.some(i=>i.pending),available:available.length>0&&available.every(i=>i.writable),error:items.find(i=>i.error)?.error??''};
}
export const titleWatchAPI={
 async read(id:string,signal?:AbortSignal):Promise<TitleWatch>{
  const v=await request(`/api/watch-titles/${encodeURIComponent(id)}`,'GET',undefined,signal) as TitleWatch;
  if(!v||typeof v.version!=='string'||!Array.isArray(v.items)||!v.items.every(i=>i&&typeof i.id==='string'&&['played','available','writable','pending'].every(k=>typeof i[k as keyof TitleWatchItem]==='boolean')&&typeof i.error==='string'&&Number.isSafeInteger(i.revision)&&i.revision>=0))throw new ApiError('Incomplete watched state. Refresh to try again.');
  return v;
 },
 async set(id:string,version:string,played:boolean,episode?:string,season?:number,dateMode?:WatchDateMode,forgetHistory=false){
  const v=await request(`/api/watch-titles/${encodeURIComponent(id)}`,'POST',{version,played,episode,season,date_mode:dateMode,...(forgetHistory?{forget_history:true}:{})}) as {status?:string};
  if(v?.status!=='accepted')throw new ApiError('Watch change was not confirmed. Refresh before retrying.');
 }
};
