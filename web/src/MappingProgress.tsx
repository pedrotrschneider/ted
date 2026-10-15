import {useEffect,useRef,useState} from 'react';
import {ApiError,errorMessage} from './api';
import {mappingOperationApi,mappingProgressText} from './mapping-operation-api';
import type{MappingOperation}from './mapping-operation-api';
export function MappingProgress({kind,id,revision,expired,completed,busy,batchCompleted}:{kind:'movies'|'series';id:string;revision:number;expired:()=>void;completed:()=>void;busy:(value:boolean)=>void;batchCompleted?:()=>void}){
 const[state,setState]=useState<MappingOperation|null>(null),[error,setError]=useState(''),[retrying,setRetrying]=useState(false),[retryRevision,setRetryRevision]=useState(0);
 const callbacks=useRef({completed,busy,expired,batchCompleted});callbacks.current={completed,busy,expired,batchCompleted};
 const seen=useRef<string|null>(null),committed=useRef<string|null>(null);
 useEffect(()=>{const c=new AbortController();let timer:ReturnType<typeof setTimeout>;
 async function poll(){try{const next=await mappingOperationApi.status(kind,id,c.signal);if(c.signal.aborted)return;setState(next);setError('');const pending=['queued','validating','publishing'].includes(next.phase);callbacks.current.busy(pending);
 if(next.phase==='complete'&&(seen.current!==null||revision>0)&&seen.current!==next.id)callbacks.current.completed();
 else if(next.committed>0&&committed.current!==`${next.id}:${next.committed}`)callbacks.current.batchCompleted?.();
 committed.current=`${next.id}:${next.committed}`;
 seen.current=pending?`pending:${next.id}`:next.id;
 if(pending)timer=setTimeout(()=>void poll(),3000);
 }catch(e){if(!c.signal.aborted){if(e instanceof ApiError&&e.status===401)callbacks.current.expired();else{setError(errorMessage(e));timer=setTimeout(()=>void poll(),5000)}}}}
 void poll();return()=>{c.abort();clearTimeout(timer)}},[kind,id,revision,retryRevision]);
 async function retry(){
  if(!state||retrying)return;
  setRetrying(true);setError('');
  try{const next=await mappingOperationApi.retry(kind,id,state.id);setState(next);callbacks.current.busy(['queued','validating','publishing'].includes(next.phase));setRetryRevision(n=>n+1)}
  catch(e){if(e instanceof ApiError&&e.status===401)callbacks.current.expired();else setError(errorMessage(e))}
  finally{setRetrying(false)}
 }
 if(!state?.id&&!error)return null;
 return <section className="card" aria-live="polite"><h2>Mapping and TED readiness</h2>{error&&<p className="notice error" role="alert">{error} Refresh status to confirm the outcome.</p>}{state&&<>
 <p>{mappingProgressText(state)}</p>
 {state.error&&<p className="notice error" role="alert">{state.error==='probe_invalid'?'A selected file has invalid technical information.':state.error==='source_changed'?'A source changed. Refresh its files and review your selections.':state.error==='mapping_conflict'?'A saved mapping or library category changed. Refresh and review your selections.':state.error==='authorization_changed'?'Administrator authorization changed. Sign in again.':'Could not validate a source. Check RD availability and server connectivity.'} {state.phase==='failed'?'The submitted selections are retained on the server. Retry unfinished batches after correcting the issue, or choose revised sources.':'TED will retry.'}</p>}
 {state.retryable&&<div className="actions"><button disabled={retrying} onClick={()=>void retry()}>{retrying?'Resuming…':'Retry unfinished batches'}</button></div>}
 {['queued','validating','publishing'].includes(state.phase)&&<p className="hint">You can close this page. Work resumes after a restart. Each batch activates in TED after every selected file passes validation.</p>}
 </>}</section>;
}
