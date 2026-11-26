import {useEffect,useRef,useState} from 'react';
import type {FormEvent} from 'react';
import {ApiError,errorMessage} from './api';
import {logsAPI} from './logs-api';
import type {LogEntry,LogSettings} from './logs-api';
const localTime=(value:Date)=>new Date(value.getTime()-value.getTimezoneOffset()*60000).toISOString().slice(0,16);
export function ClientLogs({expired}:{expired:()=>void}){
 const [entries,setEntries]=useState<LogEntry[]>([]),[cursor,setCursor]=useState<string>(),[query,setQuery]=useState(()=>new URLSearchParams({since:new Date(Date.now()-86400000).toISOString(),limit:'200'}));
 const [error,setError]=useState(''),[loading,setLoading]=useState(false),[follow,setFollow]=useState(false);
 const [settings,setSettings]=useState<LogSettings>(),[days,setDays]=useState(90),[settingsError,setSettingsError]=useState(''),[saving,setSaving]=useState(false),[confirm,setConfirm]=useState(false);
 const current=useRef(0),abort=useRef<AbortController|undefined>(undefined),alive=useRef(true);
 async function load(params:URLSearchParams,append=false){
  const generation=++current.current;abort.current?.abort();const op=new AbortController();abort.current=op;setLoading(true);setError('');
  try{const page=await logsAPI.query(params,op.signal);if(alive.current&&generation===current.current){setEntries(old=>append?[...old,...page.entries]:page.entries);setCursor(page.next_cursor);}}
  catch(e){if(alive.current&&!op.signal.aborted&&generation===current.current){if(e instanceof ApiError&&e.status===401)expired();else setError(errorMessage(e));}}
  finally{if(alive.current&&generation===current.current)setLoading(false);}
 }
 useEffect(()=>{alive.current=true;const op=new AbortController();void logsAPI.settings(op.signal).then(v=>{if(!op.signal.aborted){setSettings(v);setDays(v.retention_days);}}).catch(e=>{if(!op.signal.aborted)setSettingsError(errorMessage(e));});return()=>{alive.current=false;op.abort();abort.current?.abort();};},[]);
 useEffect(()=>{void load(query);if(!follow)return;const timer=setInterval(()=>void load(query),15000);return()=>clearInterval(timer);},[query,follow]);
 function search(e:FormEvent<HTMLFormElement>){e.preventDefault();const form=new FormData(e.currentTarget),params=new URLSearchParams({limit:'200'});for(const key of ['q','level','user_id','device_id','component','session_id','platform','app_version']){const value=String(form.get(key)??'').trim();if(value)params.set(key,value);}for(const key of ['since','until']){const value=String(form.get(key)??'');if(value)params.set(key,new Date(value).toISOString());}setQuery(params);}
 async function save(){setSaving(true);setSettingsError('');try{const value=await logsAPI.retention(days);if(alive.current){setSettings(value);setConfirm(false);void load(query);}}catch(e){if(alive.current){if(e instanceof ApiError&&e.status===401)expired();else setSettingsError(errorMessage(e));}}finally{if(alive.current)setSaving(false);}}
 return <><div className="page-heading"><p className="eyebrow">ADMINISTRATION</p><h1>Client logs</h1><p>Diagnostics uploaded by Teddy devices. Log access is restricted to administrators.</p></div>
 <div className="panel-stack logs-panels"><section className="card"><form className="logs-query-form" onSubmit={search}><fieldset disabled={loading}><div className="logs-filters">
 <label>Search<input name="q" maxLength={200} placeholder="Message contains…"/></label>
 <label>Level<select name="level"><option value="">All levels</option>{['debug','info','warning','error','fatal'].map(v=><option key={v}>{v}</option>)}</select></label>
 <label>From<input name="since" type="datetime-local" defaultValue={localTime(new Date(Date.now()-86400000))}/></label>
 <label>Until<input name="until" type="datetime-local"/></label>
 <label>Account ID<input name="user_id" type="number" min={1}/></label><label>Device ID<input name="device_id" maxLength={128}/></label>
 <label>Component<input name="component" maxLength={64}/></label><label>Playback session<input name="session_id" maxLength={36}/></label>
 <label>Platform<select name="platform"><option value="">All platforms</option>{['android','macos','windows','linux','ios'].map(v=><option key={v}>{v}</option>)}</select></label>
 <label>App version<input name="app_version" maxLength={64}/></label></div><div className="actions"><button type="submit">Query logs</button><button type="button" className="subtle" onClick={()=>void load(query)}>Refresh</button><a href={`/api/admin/logs?${new URLSearchParams([...query,['format','ndjson']])}`} download="teddy-logs.ndjson">Download this query page</a></div></fieldset></form>
 <label className="logs-follow"><input type="checkbox" checked={follow} onChange={e=>setFollow(e.target.checked)}/> Auto refresh every 15 seconds</label>
 {error&&<p className="notice error" role="alert">{error}</p>}{loading&&<p role="status">Loading logs…</p>}
 <p className="hint">Newest received batches first. The query window uses TED receipt time; displayed timestamps come from each device. Queries and downloads return bounded pages.</p>
 {!loading&&entries.length===0&&<p>No matching logs.</p>}
 <div className="client-logs">{entries.map(e=><article className="log-entry" key={`${e.batch_seq}:${e.id}`}><div><strong>{e.level.toUpperCase()}</strong> <time dateTime={e.at}>{new Date(e.at).toLocaleString()}</time> · {e.component}</div><small>{e.username} (account {e.user_id}) · {e.device_name} · {e.platform} {e.app_version} · <code>{e.device_id}</code>{e.session_id&&<> · session <code>{e.session_id}</code></>}</small><pre>{e.message}</pre></article>)}</div>
 {cursor&&<button className="subtle" disabled={loading} onClick={()=>{const next=new URLSearchParams(query);next.set('cursor',cursor);void load(next,true);}}>Load older results</button>}</section>
 <section className="card"><h2>Retention</h2><p>Logs are kept for up to the selected number of days, within the server and per-device storage limits. Changing this setting applies immediately.</p><form onSubmit={e=>{e.preventDefault();if(settings&&days<settings.retention_days)setConfirm(true);else void save();}}><fieldset className="logs-retention-fields" disabled={saving||!settings}><label>Retention in days<input type="number" min={1} max={3650} required value={days} onChange={e=>{setDays(Number(e.target.value));setConfirm(false);}}/></label><button type="submit">Save retention</button></fieldset></form>
 {settings&&<p className="hint">Stored: {(settings.stored_bytes/1048576).toFixed(1)} MiB · server limit: {settings.max_stored_bytes/1048576} MiB · per-device limit: {settings.max_device_bytes/1048576} MiB. Default retention is 90 days.</p>}
 {confirm&&<div className="confirmation"><p>Apply {days} days and permanently remove older diagnostics?</p><button disabled={saving} onClick={()=>void save()}>Apply and remove older logs</button> <button className="subtle" onClick={()=>setConfirm(false)}>Cancel</button></div>}
 {settingsError&&<p role="alert" className="notice error">{settingsError}</p>}</section></div></>;
}
