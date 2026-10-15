import {ApiError,request} from './api.ts';

export interface Artwork {
 id:string; entity_id:string; name:string; kind:'movie'|'series'|'season'; provider:'Tmdb'|'Tvdb';
 type:string; index:number; status:'new'|'queued'|'ready'|'failed'; tag?:string; error_code?:string;
 url:string; generation:number; user_modified:boolean; has_source:boolean;
}
export interface ArtworkPage {images:Artwork[];metadata_pending:boolean;metadata_cached:boolean;metadata_status:string;metadata_error:string}
export interface ArtworkChoice {id:string;type:string;url:string;preview:string;language:string;width:number;height:number}
export interface ArtworkChoices {status:'new'|'queued'|'ready'|'failed';generation:number;images:ArtworkChoice[];error_code:string}
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object';
const uuid=(v:unknown):v is string=>typeof v==='string'&&/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(v);
const types=['Primary','Logo','Banner','Backdrop'];
const statuses=['new','queued','ready','failed'];
const invalid=()=>new ApiError('TED returned incomplete artwork. Refresh the picker before saving.');
function confirmed(v:unknown,status:'queued'|'saved'){
 if(!object(v)||v.status!==status)throw new ApiError('TED did not confirm the artwork change. Refresh the picker to check its state.');
 return status;
}
function publicImage(raw:unknown):raw is string{
 if(typeof raw!=='string'||raw.length>4096)return false;
 try{const u=new URL(raw);return u.protocol==='https:'&&!u.username&&!u.password&&!u.port&&!u.hash&&['image.tmdb.org','artworks.thetvdb.com','banners.thetvdb.com'].includes(u.hostname)}catch{return false}
}
export function artworkPage(v:unknown):ArtworkPage{
 if(!object(v)||!Array.isArray(v.images)||typeof v.metadata_pending!=='boolean'||typeof v.metadata_cached!=='boolean'||typeof v.metadata_status!=='string'||typeof v.metadata_error!=='string')throw invalid();
 const images=v.images.map(a=>{
  if(!object(a)||!uuid(a.id)||!uuid(a.entity_id)||!types.includes(String(a.type))||!['movie','series','season'].includes(String(a.kind))||(a.kind==='season'&&a.type!=='Primary')||!['Tmdb','Tvdb'].includes(String(a.provider))||typeof a.name!=='string'||a.index!==0||!statuses.includes(String(a.status))||a.url!==`/api/metadata/images/${a.id}`||!Number.isSafeInteger(a.generation)||Number(a.generation)<1||typeof a.user_modified!=='boolean'||typeof a.has_source!=='boolean'||(a.tag!==undefined&&(typeof a.tag!=='string'||!/^([a-f0-9]{64})?$/.test(a.tag))))throw invalid();
  return a as unknown as Artwork;
 });
 if(new Set(images.map(a=>a.id)).size!==images.length||new Set(images.map(a=>`${a.entity_id}:${a.type}`)).size!==images.length)throw invalid();
 return {images,metadata_pending:v.metadata_pending,metadata_cached:v.metadata_cached,metadata_status:v.metadata_status,metadata_error:v.metadata_error};
}
export function artworkChoices(v:unknown):ArtworkChoices{
 if(!object(v)||!statuses.includes(String(v.status))||!Number.isSafeInteger(v.generation)||Number(v.generation)<0||!Array.isArray(v.images)||v.images.length>5000||typeof v.error_code!=='string')throw invalid();
 const images=v.images.map(a=>{
  if(!object(a)||!uuid(a.id)||!types.includes(String(a.type))||!publicImage(a.url)||!publicImage(a.preview)||typeof a.language!=='string'||!Number.isSafeInteger(a.width)||!Number.isSafeInteger(a.height)||Number(a.width)<0||Number(a.height)<0)throw invalid();
  return a as unknown as ArtworkChoice;
 });
 if(new Set(images.map(a=>a.id)).size!==images.length)throw invalid();
 return {...v,images} as unknown as ArtworkChoices;
}
export const artworkApi={
 list:async(kind:'movie'|'series',id:string,retry=false,signal?:AbortSignal)=>artworkPage(await request(`/api/admin/titles/${kind}/${encodeURIComponent(id)}/artwork${retry?'?retry=1':''}`,'GET',undefined,signal)),
 choices:async(id:string,refresh=false,signal?:AbortSignal)=>artworkChoices(await request(`/api/admin/metadata/items/${encodeURIComponent(id)}/artwork-choices${refresh?'?refresh=1':''}`,'GET',undefined,signal)),
 select:async(a:Artwork,choice:ArtworkChoice)=>confirmed(await request(`/api/admin/artwork/${a.id}/select`,'POST',{generation:a.generation,choice_id:choice.id}),'queued'),
 upload:async(a:Artwork,file:File)=>{
  if(!file.size||file.size>20*1024*1024)throw new ApiError('Choose an image up to 20 MiB.');
  return confirmed(await request(`/api/admin/artwork/${a.id}/upload?generation=${a.generation}`,'POST',file),'saved');
 },
};
