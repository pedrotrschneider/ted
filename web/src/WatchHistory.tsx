import { useEffect, useRef, useState } from 'react';
import { ApiError, errorMessage } from './api';
import { watchAPI } from './watch-api';
import {historyDate} from './watch-history-date';
import type { WatchHistory as History, WatchState, WatchCurrent } from './watch-api';
const labels:Record<string,string>={watch_date_corrected:'Watch date corrected',stremio_observed:'Watched-state change observed in Stremio',stremio_import:'Imported from Stremio',ted_watched:'Marked watched',ted_unwatched:'Marked unwatched',ted_reset:'Progress reset',ted_restore:'Saved state restored',kept_imported:'Imported state retained',baseline:'Existing state imported',manual_watched:'Marked watched',manual_unwatched:'Marked unwatched',playback_started:'Playback started',playback_stopped:'Playback stopped',playback_completed:'Playback completed',external_edit:'Imported state edited',state_observed:'State observed'};
function describe(s:WatchState){const seconds=Math.floor(s.position_ticks/10000000);return `${s.played?'Watched':'Unwatched'}${seconds>0?` · Resume ${Math.floor(seconds/60)}:${String(seconds%60).padStart(2,'0')}`:''}`;}
export function WatchHistory({expired}:{expired:()=>void}){
 const [value,setValue]=useState<History>(),[error,setError]=useState(''),[busy,setBusy]=useState(false);
 const read=useRef<AbortController|null>(null);
 async function load(before?:number){read.current?.abort();const c=new AbortController();read.current=c;setBusy(true);setError('');try{const v=await watchAPI.history(before,c.signal);if(!c.signal.aborted)setValue(old=>({...v,entries:before&&old?[...old.entries,...v.entries]:v.entries}));}catch(e){if(!c.signal.aborted){if(e instanceof ApiError&&e.status===401)expired();else setError(errorMessage(e));}}finally{if(!c.signal.aborted)setBusy(false);}}
 useEffect(()=>{void load();return()=>read.current?.abort();},[]);
 return <><div className="page-heading"><p className="eyebrow">YOUR VIEWING</p><h1>Watch history</h1><p>Saved independently of your library and media files.</p><button disabled={busy} onClick={()=>void load()}>{busy?'Refreshing…':'Refresh'}</button></div>{error&&<p className="notice error" role="alert">{error}</p>}{value&&<>
 <p className="quiet">Playback is recorded automatically. Viewing dates are shown when known; imported or detected times indicate when TED received a change.</p>
 <div className="watch-history-panels"><section className="card"><h2>Recently saved state</h2><p className="hint">Up to 50 recent items.</p>{value.current.length===0?<p>No viewing state has been saved yet.</p>:<ul className="watch-list">{value.current.map(e=><li key={e.media_id}><strong>{e.title}</strong><span>{describe(e.state)}</span><WatchActions item={e} refresh={()=>load()} expired={expired}/></li>)}</ul>}</section>
 <section className="card"><h2>History</h2>{value.entries.length===0?<p>No viewing history yet.</p>:<ol className="watch-list">{value.entries.map(e=><li key={e.id}><strong>{e.title}</strong><span>{labels[e.kind]??'Viewing state observed'} · {describe(e.state)}</span><span>{historyDate(e)}</span></li>)}</ol>}{value.next_before!==undefined&&<button disabled={busy} onClick={()=>void load(value.next_before)}>Load earlier history</button>}</section></div>
 </>}</>;
}
function WatchActions({item,refresh,expired}:{item:WatchCurrent;refresh:()=>Promise<void>;expired:()=>void}){
 const [action,setAction]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState('');
 const active=useRef(true),writing=useRef(false),selected=useRef(item.revision);
 useEffect(()=>{setAction('')},[item.revision]);
 useEffect(()=>{active.current=true;return()=>{active.current=false}},[]);
 const names:Record<string,string>={watched:'Watched now',watched_undated:'Watched without a date',unwatched:'Mark unwatched',reset:'Reset progress'};
 async function submit(){if(writing.current)return;writing.current=true;setBusy(true);setError('');try{await watchAPI.edit(item.media_id,selected.current,action==='watched_undated'?'watched':action,action==='watched_undated'?'undated':action==='watched'?'now':undefined);if(active.current){setAction('');setNotice('Viewing state saved.');await refresh();}}catch(e){if(active.current){if(e instanceof ApiError&&e.status===401)expired();else setError(errorMessage(e));}}finally{writing.current=false;if(active.current)setBusy(false);}}
 return <div className="watch-actions">{error&&<p role="alert" className="notice error">{error}</p>}{notice&&<p role="status">{notice}</p>}
 <details className="watch-edit"><summary>Edit<span className="sr-only"> viewing state for {item.title}</span></summary><div className="actions">{Object.entries(names).map(([key,name])=><button key={key} disabled={busy} onClick={()=>{selected.current=item.revision;setAction(key);setNotice('');setError('')}}>{name}</button>)}</div></details>
 {action&&<div className="confirmation"><p>{names[action]} for “{item.title}”? {action==='unwatched'?'This clears resume position, play count and last-played date.':action==='reset'?'This clears resume position and preserves the watched flag.':action==='watched_undated'?'This marks the item watched without a viewing date and clears resume position. Earlier history is retained.':'This records the current time as the viewing date and clears resume position.'}</p><div className="actions"><button disabled={busy} onClick={()=>void submit()}>Confirm</button><button disabled={busy} onClick={()=>setAction('')}>Cancel</button></div></div>}
 </div>;
}
