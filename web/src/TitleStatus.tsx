import {RatingBadge,SavedIcon} from './RatingIcon';
import {ratingNames} from './personal-rating';
import {useEffect,useLayoutEffect,useRef,useState} from 'react';
import type {CSSProperties} from 'react';
import {titleStatuses,titleStatusLabel,showTitleCount,titleStatusProgress,watchLabel,watchIndicatorText,userWatchLabel,userWatchProgress} from './title-status-api';
import type {TitleStatus as Status,UserWatch} from './title-status-api';
import type {MediaTitle} from './media-browser-api';
import {peopleWatchLayout} from './people-watch-layout';
export function useTitleStatuses(titles:MediaTitle[]){
 const key=JSON.stringify(titles.map(t=>[t.type,t.id]));
 const [epoch,setEpoch]=useState(0);
 useEffect(()=>{const changed=()=>setEpoch(v=>v+1);window.addEventListener('ted-preferences-changed',changed);return()=>window.removeEventListener('ted-preferences-changed',changed)},[]);
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
 },[key,epoch]);
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
 {status.watchlist&&<span className="watchlist-badge" role="img" aria-label="In your Watch List" title="In your Watch List"><SavedIcon/></span>}
 </div>{status.user_rating!==null&&<span className="poster-personal-rating"><RatingBadge rating={status.user_rating}/></span>}<div className="poster-indicators">
 {[{name:'You',color:'#FFFFFF',watch:status.watch,rating:null},...(status.joint_watches??[])].filter(j=>j.watch.seen).map((j,i)=><div key={i} className="title-status title-status-watched joint-watch" role="img" aria-label={`${j.name}: ${watchLabel(j.watch,kind)}`} title={`${j.name}: ${watchLabel(j.watch,kind)}`}>
 <span className="title-status-icon" style={{backgroundColor:j.color,color:'#111'}} aria-hidden="true">{watchIndicatorText(j.watch,kind)}</span>
 {j.rating!=null&&<span className="other-user-rating"><RatingBadge rating={j.rating} small/></span>}
 </div>)}
 <OtherUserIndicators users={(status.user_watches??[]).filter(user=>user.watch.seen)} kind={kind}/>
 </div>
 {kind==='movie'&&status.watch.seen&&status.watch.progress!==null&&<div className="poster-watch-progress" role="progressbar" aria-label="Movie playback progress" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(status.watch.progress)} title={viewing}><span style={{width:`${status.watch.progress}%`}}/></div>}
 </>;
}

function OtherUserIndicators({users,kind}:{users:UserWatch[];kind:'movie'|'series'}){
 const root=useRef<HTMLDivElement>(null),normal=useRef<HTMLSpanElement>(null),minimum=useRef<HTMLSpanElement>(null);
 const [layout,setLayout]=useState(()=>peopleWatchLayout(users.length,0,30,24,5));
 useLayoutEffect(()=>{
  const group=root.current,poster=group?.closest('.title-poster');
  if(!group||!poster)return;
  const measure=()=>{
   const height=normal.current?.getBoundingClientRect().height??30,min=minimum.current?.getBoundingClientRect().height??24;
   const gap=parseFloat(getComputedStyle(group).rowGap)||0;
   const available=poster.getBoundingClientRect().bottom-group.getBoundingClientRect().top-6;
   const next=peopleWatchLayout(users.length,available,height,min,gap);
   setLayout(old=>old.visible===next.visible&&old.hidden===next.hidden&&old.height===next.height&&old.offset===next.offset?old:next);
  };
  measure();
  const observer=new ResizeObserver(measure);
  observer.observe(poster);observer.observe(group.parentElement!);
  if(normal.current)observer.observe(normal.current);
  if(minimum.current)observer.observe(minimum.current);
  return()=>observer.disconnect();
 },[users.length]);
 if(!users.length)return null;
 const overflow=`${layout.hidden} more ${layout.hidden===1?'person':'people'} watched this media`;
 return <div ref={root} className="other-user-indicators" style={{'--person-badge-height':`${layout.height}px`} as CSSProperties}>
  <span ref={normal} className="person-badge-measure" aria-hidden="true"/>
  <span ref={minimum} className="person-badge-measure person-badge-minimum" aria-hidden="true"/>
  <div className="other-user-column" style={{transform:`translateY(${layout.offset}px)`}}>
   {users.slice(0,layout.visible).map(user=><OtherUserBadge key={user.account_id} user={user} kind={kind}/>)}
   {layout.hidden>0&&<div className="other-user-watch other-user-overflow" role="img" aria-label={overflow} title={overflow}>+{layout.hidden}</div>}
  </div>
 </div>;
}

function OtherUserBadge({user,kind}:{user:UserWatch;kind:'movie'|'series'}){
 const [failed,setFailed]=useState('');
 const version=`${user.account_id}/${user.avatar_version??''}`;
 const label=userWatchLabel(user,kind)+(user.rating===null?'':` · ${ratingNames[user.rating-1]}`),progress=userWatchProgress(user,kind);
 return <div className="other-user-watch" role="img" aria-label={label} title={label}>
  <span className="other-user-profile"><span className="other-user-avatar" aria-hidden="true">
   {user.avatar_version&&failed!==version?<img src={`/api/accounts/${user.account_id}/avatar?v=${user.avatar_version}`} alt="" onError={()=>setFailed(version)}/>:user.name.trim().slice(0,1).toUpperCase()}
  </span>
  {user.rating!==null&&<span className="other-user-rating"><RatingBadge rating={user.rating} small/></span>}
  </span>
  {progress&&<span className="other-user-progress" aria-hidden="true">{progress}</span>}
 </div>;
}
