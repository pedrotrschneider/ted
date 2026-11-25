import {request} from './api.ts';
export interface LogEntry {id:string;at:string;level:string;component:string;message:string;session_id?:string;user_id:number;username:string;device_id:string;device_name:string;platform:string;app_version:string;received_at:string;batch_seq:number}
export interface LogPage {entries:LogEntry[];next_cursor?:string;head_seq:number}
export interface LogSettings {retention_days:number;stored_bytes:number;max_stored_bytes:number;max_device_bytes:number}
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null;
export function logPage(v:unknown):LogPage {
 if(!object(v)||!Array.isArray(v.entries)||typeof v.head_seq!=='number'||v.next_cursor!==undefined&&typeof v.next_cursor!=='string')throw new Error('Invalid log response');
 for(const e of v.entries)if(!object(e)||!['id','at','level','component','message','username','device_id','device_name','platform','app_version','received_at'].every(k=>typeof e[k]==='string')||typeof e.user_id!=='number'||typeof e.batch_seq!=='number'||e.session_id!==undefined&&typeof e.session_id!=='string')throw new Error('Invalid log entry');
 return v as unknown as LogPage;
}
export function logSettings(v:unknown):LogSettings {
 if(!object(v)||!['retention_days','stored_bytes','max_stored_bytes','max_device_bytes'].every(k=>typeof v[k]==='number'&&Number.isSafeInteger(v[k])&&Number(v[k])>=0)||Number(v.retention_days)<1)throw new Error('Invalid log settings');
 return v as unknown as LogSettings;
}
export const logsAPI={
 query:async(params:URLSearchParams,signal?:AbortSignal)=>logPage(await request(`/api/admin/logs?${params}`,'GET',undefined,signal)),
 settings:async(signal?:AbortSignal)=>logSettings(await request('/api/admin/logs/settings','GET',undefined,signal)),
 retention:async(days:number)=>logSettings(await request('/api/admin/logs/settings','PUT',{retention_days:days})),
};
