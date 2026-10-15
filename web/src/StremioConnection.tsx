import {useEffect,useRef,useState} from 'react';
import {ApiError,errorMessage} from './api';
import {stremioApi} from './stremio-api';
import type {StremioStatus} from './stremio-api';
const issues:Record<string,string>={delivery:'Saved changes could not be applied yet. TED will resume automatically.',identity_review:'Episode or title identity could not be resolved safely.',incomplete:'Stremio supplied incomplete watched data.',changed:'Stremio changed during the read. TED will retry.',credentials:'Stremio rejected the connection. Disconnect and link again.',unavailable:'Stremio or Cinemeta could not be read.'};
export function StremioConnection({expired}:{expired:()=>void}){
 const [status,setStatus]=useState<StremioStatus|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false),[loading,setLoading]=useState(false);
 const read=useRef<AbortController|null>(null),writing=useRef(false);
 async function load(){
  read.current?.abort();const c=new AbortController();read.current=c;setLoading(true);
  try{const v=await stremioApi.status(c.signal);if(!c.signal.aborted){setError('');setStatus(v)}}catch(e){if(!c.signal.aborted){setError(errorMessage(e));if(e instanceof ApiError&&e.status===401)expired()}}finally{if(!c.signal.aborted)setLoading(false)}
 }
 useEffect(()=>{void load();return()=>read.current?.abort()},[]);
 useEffect(()=>{if(!status?.linked||busy)return;const timer=setTimeout(()=>void load(),15000);return()=>clearTimeout(timer)},[status,busy]);
 async function act(f:()=>Promise<void>){if(writing.current)return;writing.current=true;setBusy(true);setError('');read.current?.abort();setLoading(false);try{await f();await load()}catch(e){setError(errorMessage(e));if(e instanceof ApiError&&e.status===401)expired()}finally{writing.current=false;setBusy(false)}}
 return <section className="card stremio-connection"><div className="section-title"><h2>Stremio watch state</h2><button disabled={busy||loading} onClick={()=>void load()}>Refresh status</button></div>
 <p>Add-ons from this connection are not used. Link your account to automatically sync all watched movies and episodes. Watched and unwatched changes sync both ways between Stremio and TED. TED checks Stremio about once a minute. Playback positions are not synchronized with Stremio.</p>
 <p className="hint">If a member and joint account link the same Stremio history account, Stremio will synchronize them indirectly. Existing TED decisions are preserved when linking. Stremio history has no viewing date. Syncing history does not add media to your library.</p>
 {error&&<p className="notice error" role="alert">{error}</p>}
 {!status&&loading&&<p role="status">Loading connection…</p>}
 {status&&!status.linked&&<form onSubmit={e=>{e.preventDefault();const form=e.currentTarget;const data=new FormData(form);const email=String(data.get('email')??'');const password=String(data.get('password')??'');form.reset();void act(()=>stremioApi.link(email,password))}}><fieldset disabled={busy}><label>Stremio email<input type="email" name="email" required maxLength={320} autoComplete="username"/></label><label>Stremio password<input type="password" name="password" required maxLength={4096} autoComplete="current-password"/></label><p className="hint">TED stores the connection token on the server, not your Stremio password. Your TED login remains separate.</p><button type="submit">Link Stremio account</button></fieldset></form>}
 {status?.linked&&<><p>Connected as <strong>{status.email}</strong></p><button disabled={busy} onClick={()=>void act(()=>stremioApi.disconnect())}>Disconnect Stremio</button>{!status.sync&&<p role="status">Preparing account sync. You can leave this page.</p>}</>}
 {status?.sync&&<div className="notice stremio-sync-status" role="status"><p>{status.sync.error?`Stremio sync is waiting: ${issues[status.sync.error]??'A read failed.'} The last accepted state is retained; retries use automatic backoff.`:status.sync.pending?'Stremio snapshot captured; applying changes.':!status.sync.last_success?'Waiting for the first account sync.':'Two-way Stremio ↔ TED watched-state sync is active.'}</p>{status.sync.last_success&&<p className="hint">Last successful read: {new Date(status.sync.last_success).toLocaleString()}.</p>}{status.sync.outgoing.length>0&&<><p>Updates to Stremio (up to 20 pending or conflicting items):</p><ul>{status.sync.outgoing.map(o=><li key={o.identity}><strong>{o.name}</strong> — mark {o.played?'watched':'unwatched'}. {o.phase==='conflict'?'Stremio changed before confirmation. Its state was retained; make the watched/unwatched choice again in TED to retry.':o.error==='incomplete'?'Stremio returned incomplete data. TED will retry without overwriting it.':o.error?`${issues[o.error]??'Delivery failed.'} TED will retry automatically.`:'Waiting for delivery and confirmation.'}</li>)}</ul></>}{status.sync.issues>0&&<p>{status.sync.issues} titles need identity review; their state is retained without guessing.</p>}</div>}
 </section>;
}
