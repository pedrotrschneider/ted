import { useEffect, useState } from 'react';
import { ApiError, errorMessage } from './api';
import { downloadMetrics } from './download-metrics';
import { localApi } from './local-api';
import type { LocalState } from './local-api';

const labels = { absent:'Streaming from Real-Debrid', queued:'Waiting to download', downloading:'Downloading', validating:'Verifying the local copy', publishing:'Making the local copy available', ready:'Playing from the local copy', failed:'Download needs attention', cancelled:'Download cancelled; partial copy retained', removing:'Removing local copy; waiting for active transfers to finish' };
const problems: Record<string,string> = {
  transfer_failed:'The transfer failed. TED retries automatically while queued; you can retry after it stops.',
  insufficient_space:'Free space is too low. Free space in the media folder and retry.',
  storage_unavailable:'Check the media folder mount and write permissions, then retry.',
  validation_failed:'The local copy did not pass verification. Remove the partial copy and download again. RD playback is still available.',
};
const amount = (n:number) => `${(n / 1073741824).toFixed(2)} GiB`;
export function LocalDownload({id,admin,expired}:{id:string;admin:boolean;expired:()=>void}) {
  const [state,setState]=useState<LocalState|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false),[revision,setRevision]=useState(0);
  useEffect(()=>{
    const controller=new AbortController();let timer:ReturnType<typeof setTimeout>;
    async function poll(){
      try { const next=await localApi.status(id,controller.signal);if(!controller.signal.aborted){setState(next);setError('');timer=setTimeout(()=>void poll(),['queued','downloading','validating','publishing','removing'].includes(next.phase)?3000:15000);} }
      catch(e){if(!controller.signal.aborted){if(e instanceof ApiError&&e.status===401)expired();else {setError(errorMessage(e));timer=setTimeout(()=>void poll(),10000);}}}
    }
    void poll();return()=>{controller.abort();clearTimeout(timer)};
  },[id,revision]);
  async function change(action:'download'|'cancel'|'remove'){
    setBusy(true);setError('');try{await localApi.change(id,action)}catch(e){if(e instanceof ApiError&&e.status===401)expired();else setError(errorMessage(e))}finally{setBusy(false);setRevision(r=>r+1)}
  }
  if(state&&!state.eligible)return null;
  return <section className="card"><h2>Local copy</h2>
    <p>Downloads continue after you close this page. TED keeps the same media identity and viewing state.</p>
    {state&&!state.configured&&<p className="notice">Configure the server media folder to enable local downloads.</p>}
    {state&&<p role="status">{labels[state.phase]}</p>}
    {state&&state.total_bytes>0&&['queued','downloading','validating','publishing','cancelled','failed'].includes(state.phase)&&<>
      <progress aria-label={state.phase==='validating'?'Verification progress':'Download progress'} max={state.total_bytes} value={state.phase==='validating'?state.validated_bytes:state.completed_bytes}/>
      <p>{amount(state.phase==='validating'?state.validated_bytes:state.completed_bytes)} / {amount(state.total_bytes)}{state.phase==='validating'?' verified':' downloaded'}</p>
    </>}
    {state?.phase==='downloading'&&<p>{error?'Download speed and estimated time remaining unavailable.':downloadMetrics(state.speed_mbps,state.remaining_seconds)}</p>}
    {state?.error&&<p className="notice error">{problems[state.error]??'The operation needs attention. Check server diagnostics.'}</p>}
    {error&&<p className="notice error" role="alert">{error}</p>}
    {state?.phase==='ready'&&<p>Removing this copy returns playback to RD. The item stays in your library.</p>}
    <div className="actions">
      {state?.configured&&['absent','failed','cancelled'].includes(state.phase)&&<button disabled={busy} onClick={()=>void change('download')}>{state.phase==='absent'?'Download locally':'Resume download'}</button>}
      {admin&&state&&['queued','downloading','validating','publishing'].includes(state.phase)&&<button disabled={busy} onClick={()=>void change('cancel')}>Cancel download</button>}
      {admin&&state&&(['ready','failed','cancelled'].includes(state.phase)||(state.phase==='removing'&&state.error))&&<button disabled={busy} onClick={()=>void change('remove')}>{state.phase==='ready'?'Remove local copy':state.phase==='removing'?'Retry removal':'Remove partial copy'}</button>}
      <button disabled={busy} onClick={()=>setRevision(r=>r+1)}>Refresh local status</button>
    </div>
  </section>;
}
