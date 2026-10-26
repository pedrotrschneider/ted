import {ApiError,request} from './api.ts';
export type MetadataProvider='Tvdb'|'Tmdb';
export interface SeriesOverrides {artwork:number;metadata:number;version:string}
export type OverrideAction='keep'|'remove';
export interface SeriesProvider {overrides:SeriesOverrides;provider:MetadataProvider;revision:number;can_change:boolean;status:string;error_code:string}
export const providerLabel=(provider:MetadataProvider)=>provider==='Tmdb'?'TMDB':'TVDB';
export const otherProvider=(provider:MetadataProvider):MetadataProvider=>provider==='Tmdb'?'Tvdb':'Tmdb';
export function seriesProvider(value:unknown):SeriesProvider {
 if(!value||typeof value!=='object')throw new ApiError('Incomplete metadata provider information.');
 const v=value as Record<string,unknown>;
 if(!['Tvdb','Tmdb'].includes(String(v.provider))||!Number.isSafeInteger(v.revision)||Number(v.revision)<0||typeof v.can_change!=='boolean'||typeof v.status!=='string'||typeof v.error_code!=='string')throw new ApiError('Incomplete metadata provider information.');
 const o=v.overrides as Partial<SeriesOverrides>|undefined;
 if(!o||!Number.isSafeInteger(o.artwork)||Number(o.artwork)<0||!Number.isSafeInteger(o.metadata)||Number(o.metadata)<0||typeof o.version!=='string'||!/^[a-f0-9]{64}$/.test(o.version))throw new ApiError('Incomplete metadata override information.');
 return v as unknown as SeriesProvider;
}
export const seriesProviderApi={
 async get(id:string,signal?:AbortSignal):Promise<SeriesProvider>{return seriesProvider(await request(`/api/series/${encodeURIComponent(id)}/metadata-provider`,'GET',undefined,signal))},
 async change(id:string,current:MetadataProvider,decision:{action:OverrideAction;version:string},signal?:AbortSignal){await request(`/api/admin/series/${encodeURIComponent(id)}/metadata-provider`,'POST',{expected_provider:current,provider:otherProvider(current),override_action:decision.action,overrides_version:decision.version},signal)},
};
