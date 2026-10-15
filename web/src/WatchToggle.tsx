import {useEffect,useId,useRef,useState} from 'react';
import {createPortal} from 'react-dom';
import {ApiError,errorMessage} from './api';
import {titleWatchAPI,watchScope} from './title-watch-api';
import type {TitleWatch,WatchDateMode} from './title-watch-api';
export function useWatchTitle(id:string,expired:()=>void){
 const [value,setValue]=useState<TitleWatch>(),[error,setError]=useState(''),[actionError,setActionError]=useState(''),[sending,setSending]=useState(false),[refresh,setRefresh]=useState(0);
 const writing=useRef(false),epoch=useRef(0),active=useRef<string|null>(id);
 useEffect(()=>{active.current=id;setValue(undefined);setActionError('');return()=>{active.current=null;epoch.current++}},[id]);
 useEffect(()=>{const c=new AbortController();let timer:ReturnType<typeof setTimeout>;
  async function poll(){const generation=epoch.current;let pending=true;try{if(writing.current)return;const v=await titleWatchAPI.read(id,c.signal);if(!c.signal.aborted&&generation===epoch.current){setValue(v);setError('');pending=v.items.length===0||v.items.some(i=>i.pending)}}catch(e){if(!c.signal.aborted&&generation===epoch.current){setValue(undefined);if(e instanceof ApiError&&e.status===401)expired();else setError(errorMessage(e))}}finally{if(!c.signal.aborted)timer=setTimeout(()=>void poll(),pending?2000:15000)}}
  void poll();return()=>{c.abort();clearTimeout(timer)};
 },[id,refresh]);
 async function toggle(episode?:string,season?:number,dateMode?:WatchDateMode,forgetHistory=false){
  if(!value||writing.current)return;
  writing.current=true;epoch.current++;setSending(true);setActionError('');
  try{await titleWatchAPI.set(id,value.version,forgetHistory?false:!watchScope(value,episode,season).played,episode,season,dateMode,forgetHistory);const v=await titleWatchAPI.read(id);if(active.current===id)setValue(v)}
  catch(e){if(active.current===id){setValue(undefined);if(e instanceof ApiError&&e.status===401)expired();else setActionError(errorMessage(e))}}
  finally{writing.current=false;if(active.current===id){setSending(false);setRefresh(n=>n+1)}}
 }
 const failed=value?.items.some(i=>i.error);
 return {value,error:actionError||error||(failed?'A watched-state change could not be confirmed. Refresh and retry, or review it in Watch history.':''),sending,toggle};
}
export type WatchTitleControl=ReturnType<typeof useWatchTitle>;
export function WatchToggle({control,label,episode,season}:{control:WatchTitleControl;label:string;episode?:string;season?:number}){
 const [choosing,setChoosing]=useState(false);
 const state=watchScope(control.value,episode,season),pending=control.sending||state.pending;
 const action=state.played?'Mark unwatched':'Mark watched';
 const title=pending?'Saving watched state':!state.available?'Waiting for title information':state.error?'Previous change could not be confirmed. Click to retry.':`${action}: ${label}`;
 return <><button type="button" className={`watch-toggle ${state.played?'watched':''}`} disabled={pending||!state.available||!control.value} aria-label={`${action}: ${label}`} aria-pressed={state.played} aria-busy={pending} title={title} onClick={()=>{if(state.played)void control.toggle(episode,season);else setChoosing(true)}}>{pending?<span className="status-spinner" aria-hidden="true"/>:<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/></svg>}</button>{choosing&&<WatchDateDialog label={label} disabled={pending||!state.available||state.played} close={()=>setChoosing(false)} choose={mode=>{setChoosing(false);void control.toggle(episode,season,mode)}}/>}</>;
}

function WatchDateDialog({label,disabled,choose,close}:{label:string;disabled:boolean;choose:(mode:WatchDateMode)=>void;close:()=>void}){
 const dialog=useRef<HTMLDialogElement>(null),heading=useId();
 useEffect(()=>{dialog.current?.showModal()},[]);
 return createPortal(<dialog ref={dialog} className="watch-date-dialog" aria-labelledby={heading} onClose={close}>
  <h2 id={heading}>Mark watched</h2><p>{label}</p>
  <p className="hint">Choose whether to record a viewing date. For a season or show, this applies only to episodes not already watched.</p>
  <div className="actions"><button autoFocus disabled={disabled} onClick={()=>choose('now')}>Watched now</button><button disabled={disabled} onClick={()=>choose('undated')}>Watched without a date</button><button onClick={close}>Cancel</button></div>
 </dialog>,document.body);
}

export function ForgetWatchButton({control,label}:{control:WatchTitleControl;label:string}){
 const [confirm,setConfirm]=useState(false);
 const state=watchScope(control.value),pending=control.sending||state.pending;
 const disabled=pending||!state.available||!control.value;
 return <><button type="button" className="watch-toggle forget-watch" aria-label={`Mark never watched: ${label}`} title="Remove viewing history and mark never watched" disabled={disabled} aria-busy={pending} onClick={()=>setConfirm(true)}>{pending?<span className="status-spinner" aria-hidden="true"/>:<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><path d="m9 9 6 6m0-6-6 6"/></svg>}</button>{confirm&&<ForgetWatchDialog label={label} disabled={disabled} close={()=>setConfirm(false)} confirm={()=>{setConfirm(false);void control.toggle(undefined,undefined,undefined,true)}}/>}</>;
}
function ForgetWatchDialog({label,disabled,confirm,close}:{label:string;disabled:boolean;confirm:()=>void;close:()=>void}){
 const dialog=useRef<HTMLDialogElement>(null),heading=useId();
 useEffect(()=>{dialog.current?.showModal()},[]);
 return createPortal(<dialog ref={dialog} className="watch-date-dialog" aria-labelledby={heading} onClose={close}>
 <h2 id={heading}>Mark never watched?</h2><p>{label}</p>
 <p>This permanently removes your TED viewing history and resume progress for this title, including all episodes and specials. It marks them unwatched in linked Stremio accounts and removes the watched indicator.</p>
 <p>Your library and files stay in place. Watching again will create new history.</p>
 <div className="actions"><button autoFocus onClick={close}>Cancel</button><button className="forget-watch" disabled={disabled} onClick={confirm}>Remove history and mark never watched</button></div>
 </dialog>,document.body);
}
