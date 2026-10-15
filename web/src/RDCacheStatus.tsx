import {useEffect,useState} from 'react';
import {ApiError,errorMessage} from './api';
import {rdApi,type RDCacheState} from './rd-api';
import './rd-cache.css';

export function RDCacheStatus({expired,revision}:{expired:()=>void;revision:number}){
 const [state,setState]=useState<RDCacheState|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false),[attempt,setAttempt]=useState(0);
 useEffect(()=>{
  const c=new AbortController();let timer:ReturnType<typeof setTimeout>|undefined;
  async function read(){try{const v=await rdApi.cache(c.signal);if(!c.signal.aborted){setState(v);setError('')}}catch(e){if(!c.signal.aborted){setError(errorMessage(e));if(e instanceof ApiError&&e.status===401)expired()}}finally{if(!c.signal.aborted)timer=setTimeout(()=>void read(),5000)}}
  void read();return()=>{c.abort();clearTimeout(timer)};
 },[revision,attempt]);
 async function refresh(full:boolean){if(busy)return;setBusy(true);setError('');try{setState(await rdApi.refresh(full));setAttempt(n=>n+1)}catch(e){setError(errorMessage(e));if(e instanceof ApiError&&e.status===401)expired()}finally{setBusy(false)}}
 const date=(v:string|null)=>v?new Date(v).toLocaleString():'Not yet completed';
 return <div className="rd-cache-status"><h3>Real-Debrid account sync</h3>
  {state&&<><p>{!state.configured?'Configure a Real-Debrid key to enable sync':state.ready?`${state.total.toLocaleString()} torrents cached`:'Building the initial torrent cache'} · {state.phase.replaceAll('_',' ')}{state.files_pending>0&&` · ${state.files_pending.toLocaleString()} file lists remaining`}</p>
   <p className="hint">Last successful check: {date(state.checked_at)}<br/>Last full rebuild: {date(state.full_rebuilt_at)}</p>
   {state.error_code&&<p className="notice error" role="alert">{state.error_code==='rd_credentials'?'Real-Debrid rejected the saved credential.':state.error_code==='rd_limited'?'Real-Debrid is rate limiting requests.':state.error_code==='rd_incomplete'?'Real-Debrid returned incomplete or changing results.':'Real-Debrid sync failed.'} Previous complete results are preserved.</p>}
  </>}
  {error&&<p className="notice error" role="alert">{error}</p>}
  <div className="rd-cache-actions"><button type="button" disabled={busy||!state?.configured} onClick={()=>void refresh(false)}>Refresh account cache</button><button type="button" disabled={busy||!state?.configured} onClick={()=>void refresh(true)}>Full cache rebuild</button></div>
  <p className="hint">Recent torrents are checked every minute; the complete account is reconciled daily. Rebuilds continue in the background.</p>
 </div>;
}
