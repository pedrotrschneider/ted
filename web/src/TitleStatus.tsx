import {useEffect,useState} from 'react';
import {titleStatuses,titleStatusLabel,showTitleCount,titleStatusProgress,watchLabel,watchIndicatorText} from './title-status-api';
import type {TitleStatus as Status} from './title-status-api';
import type {MediaTitle} from './media-browser-api';
export function useTitleStatuses(titles:MediaTitle[]){
 const key=JSON.stringify(titles.map(t=>[t.type,t.id]));
 const [states,setStates]=useState<Record<string,Status>>({}),[error,setError]=useState('');
 useEffect(()=>{
  const c=new AbortController();let timer:ReturnType<typeof setTimeout>;
  const pairs=JSON.parse(key) as ['movie'|'series',string][];
  async function refresh(){
   let pending=false;
   try{
    const results=await Promise.all((['movie','series'] as const).map(kind=>titleStatuses(kind,pairs.filter(p=>p[0]===kind).map(p=>p[1]),c.signal)));
    pending=results.flat().some(s=>s.state==='pending');
    if(!c.signal.aborted){setStates(Object.fromEntries(results.flat().map(s=>[s.id,s])));setError('')}
   }catch{if(!c.signal.aborted)setError('Cannot refresh poster status. Previous indicators are retained.')}
   finally{if(!c.signal.aborted)timer=setTimeout(refresh,pending?1000:3000)}
  }
  if(pairs.length)void refresh();
  return()=>{c.abort();clearTimeout(timer)};
 },[key]);
 return {states,error};
}
export function TitleStatusBadge({status,kind}:{status?:Status;kind:'movie'|'series'}){
 if(!status)return null;
 const label=titleStatusLabel(status),viewing=watchLabel(status.watch,kind);
 return <><div className="poster-publication">
 {status.state&&<div className={`title-status title-status-${status.state}`} role="img" aria-label={label} title={label}>
  <span className="title-status-icon" aria-hidden="true">{status.state==='pending'?<i className="status-spinner"/>:status.state==='failed'?'!':'✓'}</span>
  {showTitleCount(status,kind)&&<span className="title-status-count" aria-hidden="true">{status.state==='pending'&&<small>{status.phase==='publishing'?'Adding':'Validating'}</small>}{titleStatusProgress(status)} / {status.total??'?'}</span>}
 </div>}
 </div><div className="poster-indicators">
 {[{name:'You',color:'#FFFFFF',watch:status.watch},...(status.joint_watches??[])].filter(j=>j.watch.seen).map((j,i)=><div key={i} className="title-status title-status-watched" role="img" aria-label={`${j.name}: ${watchLabel(j.watch,kind)}`} title={`${j.name}: ${watchLabel(j.watch,kind)}`}>
 <span className="title-status-icon" style={{backgroundColor:j.color,color:'#111'}} aria-hidden="true">{watchIndicatorText(j.watch,kind)}</span>
 </div>)}
 </div>
 {kind==='movie'&&status.watch.seen&&status.watch.progress!==null&&<div className="poster-watch-progress" role="progressbar" aria-label="Movie playback progress" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(status.watch.progress)} title={viewing}><span style={{width:`${status.watch.progress}%`}}/></div>}
 </>;
}
