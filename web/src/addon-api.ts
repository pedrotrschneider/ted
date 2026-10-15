import {ApiError,request} from './api.ts';
export interface AddonPolicy {id:string;name:string;enabled:boolean;revision:number}
function policy(value:unknown):AddonPolicy {
 if(typeof value!=='object'||value===null)throw new ApiError('TED returned invalid add-on settings.');
 const v=value as Record<string,unknown>;
 if(typeof v.id!=='string'||!/^[a-f0-9]{64}$/.test(v.id)||typeof v.name!=='string'||v.name.length>256||typeof v.enabled!=='boolean'||!Number.isSafeInteger(v.revision)||Number(v.revision)<0)throw new ApiError('TED returned invalid add-on settings.');
 return {id:v.id,name:v.name,enabled:v.enabled,revision:Number(v.revision)};
}
export const addonApi={
 list:async(signal?:AbortSignal,refresh=false)=>{const v=await request('/api/admin/addons'+(refresh?'?refresh=1':''),'GET',undefined,signal) as {addons?:unknown};if(!v||!Array.isArray(v.addons))throw new ApiError('TED returned invalid add-on settings.');const values=v.addons.map(policy);if(new Set(values.map(a=>a.id)).size!==values.length)throw new ApiError('TED returned duplicate add-on settings.');return values},
 save:async(current:AddonPolicy,enabled:boolean)=>policy(await request('/api/admin/addons','PUT',{id:current.id,revision:current.revision,enabled})),
};
