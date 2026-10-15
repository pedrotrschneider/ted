import {ApiError,request} from './api.ts';
export interface ProviderStatus {revision:number;rd_configured:boolean;tmdb_configured:boolean;tvdb_configured:boolean;tvdb_pin_configured:boolean}
export type ProviderPatch = {revision:number;rd_key?:string;tmdb_token?:string;tvdb_key?:string;tvdb_pin?:string};
function status(value:unknown):ProviderStatus {
 if(typeof value!=='object'||value===null)throw new ApiError('TED returned invalid provider settings. Reload their status.');
 const v=value as Record<string,unknown>;
 if(!Number.isSafeInteger(v.revision)||Number(v.revision)<1||['rd_configured','tmdb_configured','tvdb_configured','tvdb_pin_configured'].some(k=>typeof v[k]!=='boolean'))throw new ApiError('TED returned invalid provider settings. Reload their status.');
 return {revision:Number(v.revision),rd_configured:Boolean(v.rd_configured),tmdb_configured:Boolean(v.tmdb_configured),tvdb_configured:Boolean(v.tvdb_configured),tvdb_pin_configured:Boolean(v.tvdb_pin_configured)};
}
export const providerApi={
 status:async(signal?:AbortSignal)=>status(await request('/api/admin/providers','GET',undefined,signal)),
 save:async(patch:ProviderPatch)=>status(await request('/api/admin/providers','PUT',patch)),
};
