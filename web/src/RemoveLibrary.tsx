import {useEffect,useRef,useState} from 'react';
import {ApiError,errorMessage} from './api';
import {libraryApi} from './library-api';
import type {LibraryRemoval} from './library-api';
export function RemoveLibrary({kind,id,name,present,disabled,expired,busy,completed}:{kind:'movies'|'series';id:string;name:string;present:boolean;disabled:boolean;expired:()=>void;busy:(value:boolean)=>void;completed:()=>void}){
 const[actionError,setActionError]=useState('');
 const[state,setState]=useState<LibraryRemoval|null>(null),[confirm,setConfirm]=useState(false),[saving,setSaving]=useState(false),[error,setError]=useState(''),[revision,setRevision]=useState(0);
 const callbacks=useRef({expired,busy,completed});callbacks.current={expired,busy,completed};
 const seen=useRef('');
 useEffect(()=>{
  const c=new AbortController();let timer:ReturnType<typeof setTimeout>;
  async function poll(){
   try{
    const next=await libraryApi.status(kind,id,c.signal);if(c.signal.aborted)return;
    setState(next);setError('');callbacks.current.busy(next.phase==='snapshot'||next.phase==='removing');
    if(next.phase==='complete'&&seen.current!==next.id){seen.current=next.id;callbacks.current.completed()}
   }catch(e){if(c.signal.aborted)return;if(e instanceof ApiError&&e.status===401){callbacks.current.expired();return}setError(errorMessage(e))}
   if(!c.signal.aborted)timer=setTimeout(()=>void poll(),3000);
  }
  void poll();return()=>{c.abort();clearTimeout(timer)};
 },[kind,id,revision]);
 async function remove(){
  setSaving(true);setActionError('');
  try{const next=await libraryApi.remove(kind,id);setState(next);setConfirm(false);callbacks.current.busy(next.phase!=='complete')}
  catch(e){if(e instanceof ApiError&&e.status===401)expired();else setActionError(errorMessage(e))}
  finally{setSaving(false);setRevision(n=>n+1)}
 }
 const pending=state?.phase==='snapshot'||state?.phase==='removing';
 if(!present&&!state?.id&&!error)return null;
 return <section className="card removal-panel">
  {state?.phase==='complete'?<p role="status">Removed from the library.</p>:pending?<><p role="status">Cleaning up TED-owned local files…</p><p className="hint">This continues on the server and waits for active file use before cleanup.</p><button disabled={saving} onClick={()=>void remove()}>Retry removal</button></>:confirm?<><p>Remove <strong>{name}</strong>{kind==='series'?' and all of its episodes':''} from TED? Downloads will stop and TED-owned local copies will be deleted. Watch history and source audit records are retained. RD account torrents are not deleted.</p><div className="actions"><button className="danger" disabled={saving||disabled} onClick={()=>void remove()}>{saving?'Removing…':'Confirm removal'}</button><button disabled={saving} onClick={()=>setConfirm(false)}>Cancel</button></div></>:<button className="danger" disabled={disabled||saving||!state} onClick={()=>setConfirm(true)}>Remove from library</button>}
  {state?.error&&<p className="notice error" role="alert">{state.error==='waiting_for_transfer'?'Waiting for active transfers to finish before deleting files.':'Local cleanup needs attention. Check the media mount and permissions; TED will retry.'}</p>}
  {(actionError||error)&&<p className="notice error" role="alert">{actionError||error}</p>}
 </section>;
}
