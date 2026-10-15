import {useEffect,useRef,useState} from 'react';
import {ApiError,errorMessage} from './api';
import {providerApi} from './provider-api';
import type {ProviderPatch,ProviderStatus} from './provider-api';
import {RDCacheStatus} from './RDCacheStatus';

const fields=[
 {key:'rd_key',flag:'rd_configured',label:'Real-Debrid API key',help:'Used for mappings, RD-backed add-on playback and downloads.',url:'https://real-debrid.com/apitoken'},
 {key:'tmdb_token',flag:'tmdb_configured',label:'TMDb API Read Access Token',help:'Movie metadata, cast, and person filmographies.',url:'https://www.themoviedb.org/settings/api'},
 {key:'tvdb_key',flag:'tvdb_configured',label:'TVDB API key',help:'Show, season, episode and cast metadata.',url:'https://thetvdb.com/dashboard/account/apikey'},
 {key:'tvdb_pin',flag:'tvdb_pin_configured',label:'TVDB subscriber PIN',help:'Only needed when your TVDB key requires a subscriber PIN.',url:'https://thetvdb.com/dashboard/account/subscription'},
] as const;
export function ProviderSettings({expired}:{expired:()=>void}) {
 const [status,setStatus]=useState<ProviderStatus|null>(null),[loading,setLoading]=useState(true),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState(''),[attempt,setAttempt]=useState(0);
 const alive=useRef(false),saving=useRef(false);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[]);
 useEffect(()=>{const c=new AbortController();setLoading(true);setError('');void providerApi.status(c.signal).then(v=>{if(!c.signal.aborted)setStatus(v)}).catch(e=>{if(!c.signal.aborted){setError(errorMessage(e));if(e instanceof ApiError&&e.status===401)expired()}}).finally(()=>{if(!c.signal.aborted)setLoading(false)});return()=>c.abort()},[attempt]);
 async function save(form:HTMLFormElement) {
  if(!status||saving.current)return;
  const patch:ProviderPatch={revision:status.revision},data=new FormData(form);
  for(const f of fields){const value=String(data.get(f.key)??'').trim();if(value)patch[f.key]=value}
  if(Object.keys(patch).length===1){setNotice('Enter a new credential to save it. Use a Remove button to delete a saved credential.');return}
  form.reset();await persist(patch,'Provider credentials saved. Changes apply to new requests immediately.');
 }
 async function persist(patch:ProviderPatch,message:string) {
  if(saving.current)return;
  saving.current=true;setBusy(true);setError('');setNotice('');
  try{const v=await providerApi.save(patch);if(alive.current){setStatus(v);setNotice(message)}}
  catch(e){if(alive.current){setError(errorMessage(e));if(e instanceof ApiError&&e.status===401)expired()}}
  finally{saving.current=false;if(alive.current)setBusy(false)}
 }
 return <section className="card provider-settings"><div className="section-title"><h2>Server providers</h2><button disabled={loading||busy} onClick={()=>{setNotice('');setAttempt(n=>n+1)}}>Reload status</button></div>
  <p>Configure the providers used by everyone on this TED server. Saved credentials stay on the server. Leave a field blank to keep its current value.</p>
  {error&&<p className="notice error" role="alert">{error}</p>}{notice&&<p className="notice" role="status">{notice}</p>}{loading&&<p role="status">Loading provider settings…</p>}
  {status&&<form onSubmit={e=>{e.preventDefault();void save(e.currentTarget)}}><fieldset disabled={busy||loading||Boolean(error)}>
   {fields.map(f=><div key={f.key}><label>{f.label} <span className="quiet">({status[f.flag]?'configured':'not configured'})</span><input type="password" name={f.key} maxLength={8192} autoComplete="new-password" spellCheck={false} placeholder={status[f.flag]?'Enter a replacement':'Enter a credential'}/></label><p className="hint">{f.help} <a href={f.url} target="_blank" rel="noreferrer">Get credential</a></p>{status[f.flag]&&<button className="danger provider-remove" type="button" onClick={()=>void persist({revision:status.revision,[f.key]:''},`Saved ${f.label} removed.`)}>Remove saved {f.label}</button>}{f.key==='rd_key'&&<RDCacheStatus expired={expired} revision={status.revision}/>}</div>)}
   <button className="primary" type="submit">{busy?'Saving…':'Save provider credentials'}</button>
  </fieldset></form>}
  <p className="hint">This product uses the TMDB API but is not endorsed or certified by TMDB. TV metadata provided by <a href="https://thetvdb.com" target="_blank" rel="noreferrer">TheTVDB</a>.</p>
 </section>;
}
