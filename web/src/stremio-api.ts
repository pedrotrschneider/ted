import {ApiError,request} from './api.ts';
export interface StremioOutgoing{identity:string;name:string;played:boolean;phase:string;error:string}
export interface StremioSync{outgoing:StremioOutgoing[];last_success:string|null;next_poll:string;error:string;pending:boolean;issues:number}
export interface StremioStatus{linked:boolean;email:string;sync:StremioSync|null}
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object';
const count=(v:unknown):v is number=>Number.isSafeInteger(v)&&Number(v)>=0;
const invalid=()=>new ApiError('Incomplete Stremio sync status. Refresh to check its current state.');
export const stremioApi={
 async status(signal?:AbortSignal):Promise<StremioStatus>{
  const v=await request('/api/stremio','GET',undefined,signal);
  if(!object(v)||typeof v.linked!=='boolean'||typeof v.email!=='string'||v.sync===undefined)throw invalid();
  if(v.sync!==null){const s=v.sync;if(!object(s)||(s.last_success!==null&&typeof s.last_success!=='string')||typeof s.next_poll!=='string'||typeof s.error!=='string'||typeof s.pending!=='boolean'||!count(s.issues)||!Array.isArray(s.outgoing)||s.outgoing.some(o=>!object(o)||typeof o.identity!=='string'||typeof o.name!=='string'||typeof o.played!=='boolean'||!['pending','prepared','conflict'].includes(String(o.phase))||typeof o.error!=='string'))throw invalid()}
  return v as unknown as StremioStatus;
 },
 async link(email:string,password:string){const v=await request('/api/stremio/link','POST',{email,password});if(!object(v)||v.status!=='linked')throw invalid()},
 async disconnect(){const v=await request('/api/stremio/link','DELETE');if(!object(v)||v.status!=='disconnected')throw invalid()},
};
