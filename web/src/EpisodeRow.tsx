import {WatchToggle} from './WatchToggle';
import type {WatchTitleControl} from './WatchToggle';
import {useEffect,useState} from 'react';
import {ApiError,errorMessage} from './api';
import {localApi} from './local-api';
import type {LocalState} from './local-api';
import {streamingApi} from './streaming-api';
import type {StreamState} from './streaming-api';
import type {Episode} from './series-api';
import {episodeDownload, readinessBadge} from './episode-status';

export function EpisodeRow({episode,mapped,admin,expired,revision,watch}:{watch:WatchTitleControl;episode:Episode;mapped:boolean|undefined;admin:boolean;expired:()=>void;revision:number}){
 const [local,setLocal]=useState<LocalState|null>(null),[stream,setStream]=useState<StreamState|null>(null);
 const [actionError,setActionError]=useState('');
 const [error,setError]=useState(''),[busy,setBusy]=useState(false),[refresh,setRefresh]=useState(0);
 useEffect(()=>{
  const controller=new AbortController();let timer:ReturnType<typeof setTimeout>;
  async function poll(){
   const results=await Promise.allSettled([localApi.status(episode.id,controller.signal),admin?streamingApi.status(episode.id,controller.signal):Promise.resolve(null)]);
   if(controller.signal.aborted)return;
   const [file,readiness]=results;
   setLocal(file.status==='fulfilled'?file.value:null);
   setStream(readiness.status==='fulfilled'?readiness.value:null);
   const failures=results.filter(r=>r.status==='rejected');
   if(failures.some(r=>r.reason instanceof ApiError&&r.reason.status===401)){expired();return;}
   setError(failures.map(r=>errorMessage(r.reason)).join(' '));
   const active=file.status==='fulfilled'&&episodeDownload(file.value).active;
   const adding=readiness.status==='fulfilled'&&readiness.value?.adding;
   timer=setTimeout(()=>void poll(),active||adding?3000:15000);
  }
  void poll();return()=>{controller.abort();clearTimeout(timer)};
 },[episode.id,admin,revision,refresh,mapped]);
 async function change(action:'download'|'cancel'|'remove'){
  setBusy(true);setActionError('');
  try{await localApi.change(episode.id,action);}
  catch(e){if(e instanceof ApiError&&e.status===401)expired();else setActionError(errorMessage(e));}
  finally{setBusy(false);setRefresh(n=>n+1);}
 }
 const download=local?episodeDownload(local):null;
 const badge=readinessBadge(stream);
 const mappingStatus=stream?.mapped??local?.mapped??mapped;
 const label=`Season ${episode.season} Episode ${episode.number}`;
 return <li className="episode-row">
  <WatchToggle control={watch} episode={episode.id} label={`Season ${episode.season} Episode ${episode.number}`}/>
  <div className="episode-title"><strong className="episode-name" title={episode.name}>{episode.number}. {episode.name}</strong>{episode.release_date?<time className="episode-release" dateTime={episode.release_date} title="Release date">{episode.release_date}</time>:<span className="episode-release">Date unknown</span>}</div>
  <div className="episode-badges">
   <span title={stream?.adding?'Source assignment accepted; the complete batch is still pending.':undefined} className={`episode-status ${mappingStatus===undefined?'unknown':mappingStatus?'success':'error'}`}>{mappingStatus===undefined?'Mapping unknown':mappingStatus?'Mapped':'Unmapped'}</span>
   {admin&&<span className={`episode-status ${badge.tone}`}>{(badge.tone==='pending'||badge.tone==='waiting')&&<span className="status-spinner" aria-hidden="true"/>}{badge.label}</span>}
   {local?.eligible&&local.phase!=='removing'&&<span className={`episode-status ${local.phase==='ready'?'local':'success'}`}>{local.phase==='ready'?'Downloaded locally':'Streaming from RD'}</span>}
  </div>
  <div className="episode-controls">
   {local?.eligible&&<>
    {download?.showProgress&&<div className="episode-progress" title={download.phaseLabel}>
     <progress aria-label={`${label}: ${download.phaseLabel}`} max={100} value={download.percent??undefined}/>
     <span>{download.percent===null?'…':`${download.percent}%`} · {local.speed_mbps===null?'— Mbps':`${local.speed_mbps.toFixed(1)} Mbps`}</span>
    </div>}
    {local.phase==='removing'&&<span role="status">Removing…</span>}
    {download?.canDownload&&<button disabled={busy||!local.configured} title={!local.configured?'Configure the server media folder':undefined} onClick={()=>void change('download')}>{local.phase==='absent'?'Download':'Resume'}</button>}
    {admin&&download?.canPause&&<button disabled={busy} onClick={()=>void change('cancel')} aria-label={`Pause download: ${label}`}>Pause</button>}
    {admin&&download?.canRemove&&<button className="episode-icon" disabled={busy} onClick={()=>void change('remove')} aria-label={`Remove local file: ${label}`} title="Stop download and remove local file"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7"/></svg></button>}
   </>}
   <button className="episode-icon" disabled={busy} onClick={()=>setRefresh(n=>n+1)} aria-label={`Refresh readiness and local status: ${label}`} title="Refresh readiness and local status"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 7v5h-5M4 17v-5h5M6 7a7 7 0 0 1 12-1l2 6M4 12l2 6a7 7 0 0 0 12-1"/></svg></button>
  </div>
  {(actionError||error||local?.error||stream?.error)&&<p className="notice error episode-error" role="alert">{actionError||error||'This operation needs attention. Check server connectivity and storage, then retry.'}</p>}
 </li>;
}
