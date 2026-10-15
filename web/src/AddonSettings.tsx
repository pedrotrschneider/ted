import {useEffect,useRef,useState} from 'react';
import {ServerAddonConnection} from './ServerAddonConnection';
import {ApiError,errorMessage} from './api';
import {addonApi} from './addon-api';
import type {AddonPolicy} from './addon-api';
export function AddonSettings({expired}:{expired:()=>void}) {
 const [addons,setAddons]=useState<AddonPolicy[]>([]),[loading,setLoading]=useState(true),[busy,setBusy]=useState(''),[error,setError]=useState(''),[attempt,setAttempt]=useState(0);
 const alive=useRef(false),saving=useRef(false);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[]);
 useEffect(()=>{const c=new AbortController();setLoading(true);setError('');void addonApi.list(c.signal,attempt>0).then(v=>{if(!c.signal.aborted)setAddons(v)}).catch(e=>{if(!c.signal.aborted){setError(errorMessage(e));if(e instanceof ApiError&&e.status===401)expired()}}).finally(()=>{if(!c.signal.aborted)setLoading(false)});return()=>c.abort()},[attempt]);
 async function save(addon:AddonPolicy,enabled:boolean){
  if(saving.current)return;saving.current=true;setBusy(addon.id);setError('');
  try{const value=await addonApi.save(addon,enabled);if(alive.current)setAddons(old=>old.map(a=>a.id===value.id?value:a))}
  catch(e){if(alive.current){setError(errorMessage(e));if(e instanceof ApiError&&e.status===401)expired()}}
  finally{saving.current=false;if(alive.current)setBusy('')}
 }
 return <section className="card"><div className="section-title"><h2>Stremio add-ons</h2><button disabled={loading||Boolean(busy)} onClick={()=>setAttempt(a=>a+1)}>Reload add-ons</button></div>
  <ServerAddonConnection expired={expired} changed={()=>setAttempt(a=>a+1)}/><p>Enable or disable source lookups for everyone on this TED server. Named variants such as Torrentio and Torrentio RD have separate switches. The list uses only the server add-on account.</p>
  {error&&<p className="notice error" role="alert">{error}</p>}{loading&&<p role="status">Loading add-ons…</p>}
  {!loading&&addons.length===0&&<p>No installed add-ons have been retrieved yet. Connect the server add-on account, then reload here.</p>}
  <ul className="episode-list addon-list" aria-label="Installed Stremio add-ons">{addons.map(a=><li className="episode-row" key={a.id}>
   <button type="button" className={`watch-toggle ${a.enabled?'watched':''}`} aria-label={`${a.enabled?'Disable':'Enable'} add-on: ${a.name}`} aria-pressed={a.enabled} aria-busy={busy===a.id} title={`${a.enabled?'Disable':'Enable'} ${a.name} for everyone`} disabled={loading||Boolean(busy)||Boolean(error)} onClick={()=>void save(a,!a.enabled)}>{busy===a.id?<span className="status-spinner" aria-hidden="true"/>:<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>{!a.enabled&&<path d="m3 3 18 18"/>}</svg>}</button>
   <div className="episode-title"><strong className="episode-name" title={a.name}>{a.name}</strong><span className="hint">{busy===a.id?'Saving…':a.enabled?'Source lookups enabled':'Source lookups disabled'}</span></div>
   <span className={`episode-status ${a.enabled?'success':'unknown'}`}>{a.enabled?'Enabled':'Disabled'}</span>
  </li>)}</ul>
  <p className="hint">Disabling an add-on blocks new source lookups and selections. Playback already using a valid link can continue. Favorites and recent sources are retained.</p>
 </section>;
}
