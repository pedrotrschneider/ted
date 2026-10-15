import {ApiError,request} from './api.ts';
export interface PosterWatch {seen:boolean;watched:number;available:number;progress:number|null}
export interface JointWatch {account_id:number;name:string;color:string;watch:PosterWatch}
export interface TitleStatus {joint_watches?:JointWatch[];publication_mode:'native'; watch:PosterWatch;id:string;state:''|'pending'|'failed'|'added';added:number;total:number|null;phase:''|'queued'|'validating'|'publishing'|'complete'|'failed';validated:number}
export function parseTitleStatuses(value:unknown,ids:string[]):TitleStatus[]{
 const v=value as {statuses?:TitleStatus[]};
 if(!v||!Array.isArray(v.statuses)||v.statuses.length!==ids.length||new Set(v.statuses.map(s=>s?.id)).size!==ids.length||v.statuses.some(s=>!s||!['native'].includes(s.publication_mode)||!validWatch(s.watch)||(s.joint_watches!==undefined&&(!Array.isArray(s.joint_watches)||s.joint_watches.some(j=>!Number.isSafeInteger(j.account_id)||typeof j.name!=='string'||!/^#[0-9a-f]{6}$/i.test(j.color)||!validWatch(j.watch))))||!ids.includes(s.id)||!['','pending','failed','added'].includes(s.state)||!['','queued','validating','publishing','complete','failed'].includes(s.phase)||!Number.isSafeInteger(s.validated)||s.validated<0||!Number.isSafeInteger(s.added)||s.added<0||(s.total!==null&&(!Number.isSafeInteger(s.total)||s.total<0))))throw new ApiError('Title status is unavailable. Previous indicators are retained.');
 return v.statuses;
}
export async function titleStatuses(kind:'movie'|'series',ids:string[],signal:AbortSignal){
 const result:TitleStatus[]=[];
 for(let i=0;i<ids.length;i+=100){const batch=ids.slice(i,i+100);result.push(...parseTitleStatuses(await request(`/api/title-statuses?kind=${kind}&ids=${batch.join(',')}`,'GET',undefined,signal),batch))}
 return result;
}
export function titleStatusLabel(s:TitleStatus){
 const label=s.state==='pending'?'Validating and adding to TED':s.state==='failed'?'TED source validation failed':s.state==='added'?'Ready in TED':'';
 if(s.state==='pending')return `${s.phase==='publishing'?'Activating in TED':'Validating files'} · ${titleStatusProgress(s)} / ${s.total??'?'} episodes in this submission`;
 return label+(s.total!==1&&s.state?` · ${s.added} / ${s.total??'?'} episodes added`:'');
}
export function showTitleCount(s:TitleStatus,kind:'movie'|'series'){return kind==='series'&&(s.state==='pending'||s.state==='added'&&(s.total===null||s.added<s.total))}

export function titleStatusProgress(s:TitleStatus){return s.state==='pending'&&s.phase!=='publishing'?s.validated:s.added}

function validWatch(w:PosterWatch){return !!w&&typeof w.seen==='boolean'&&Number.isSafeInteger(w.watched)&&w.watched>=0&&Number.isSafeInteger(w.available)&&w.available>=w.watched&&(w.progress===null||(Number.isFinite(w.progress)&&w.progress>=0&&w.progress<=100))}
export function showWatchCount(w:PosterWatch,kind:'movie'|'series'){return kind==='series'&&w.seen&&w.watched!==w.available}
export function watchLabel(w:PosterWatch,kind:'movie'|'series'){return kind==='series'?`Previously viewed · ${w.watched} / ${w.available} released main-season episodes fully watched`:`Previously viewed${w.progress===null?'':` · ${Math.round(w.progress)}% playback progress`}`}

export function watchIndicatorText(w:PosterWatch,kind:'movie'|'series') {
 return kind==='series'&&w.watched<w.available ? `${w.watched} / ${w.available}` : w.watched>0 ? '✓' : '◷';
}
