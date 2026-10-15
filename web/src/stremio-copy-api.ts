import {ApiError,request} from './api.ts';
export type CopyPhase='queued'|'resolving'|'copying'|'retrying'|'failed'|'complete'|'cancelled';
export interface CopySkip{id:string;name:string;reason:string}
export interface StremioCopy{id:string;source_email:string;destination_email:string;phase:CopyPhase;error:string;error_title:string;total:number;resolved:number;finished:number;watched_titles:number;watched_items:number;skipped:CopySkip[];next_attempt:string}
export interface CopyCredentials{email:string;password:string}
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const uuid=(v:unknown):v is string=>typeof v==='string'&&/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(v);
const count=(v:unknown)=>Number.isSafeInteger(v)&&Number(v)>=0;
const incomplete=()=>new ApiError('Incomplete copy status. Refresh to check saved progress.');
export const copyActive=(v:StremioCopy|null)=>!!v&&!['complete','cancelled'].includes(v.phase);
export const copyApi={
 async status(signal?:AbortSignal):Promise<StremioCopy|null>{
  const data=await request('/api/admin/stremio-copy','GET',undefined,signal);
  if(!object(data)||data.copy===undefined)throw incomplete();
  if(data.copy===null)return null;
  const v=data.copy;
  if(!object(v)||!uuid(v.id)||!['queued','resolving','copying','retrying','failed','complete','cancelled'].includes(String(v.phase))||['source_email','destination_email','error','error_title','next_attempt'].some(k=>typeof v[k]!=='string')||['total','resolved','finished','watched_titles','watched_items'].some(k=>!count(v[k]))||!Array.isArray(v.skipped)||v.skipped.some(x=>!object(x)||typeof x.id!=='string'||typeof x.name!=='string'||typeof x.reason!=='string'))throw incomplete();
  return v as unknown as StremioCopy;
 },
 async start(source:CopyCredentials,destination:CopyCredentials){const v=await request('/api/admin/stremio-copy','POST',{source,destination});if(!object(v)||!uuid(v.id))throw incomplete();return v.id},
 async control(id:string,action:'retry'|'cancel'){const v=await request('/api/admin/stremio-copy/'+encodeURIComponent(id)+'/'+action,'POST',{});if(!object(v)||v.status!==action)throw incomplete()},
};
