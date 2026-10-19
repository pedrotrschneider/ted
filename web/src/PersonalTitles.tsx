import {useEffect,useRef,useState} from 'react';
import {ApiError} from './api';
import {collection,setTitlePreference} from './personal-titles-api';
import type {MediaTitle} from './media-browser-api';
import {MediaGrid} from './MediaPresentation';

import {ratingNames} from './personal-rating';
import {RatingBadge,SavedIcon} from './RatingIcon';
export function PersonalControls({title,watchlist,rating}:{title:MediaTitle;watchlist:boolean;rating:number|null}){
 const [busy,setBusy]=useState(false),[error,setError]=useState('');const dialog=useRef<HTMLDialogElement>(null);
 async function update(field:'watchlist'|'rating',value:boolean|number|null){
  setBusy(true);setError('');try{await setTitlePreference(title,field,value);window.dispatchEvent(new Event('ted-preferences-changed'));dialog.current?.close()}catch(e){setError(e instanceof Error?e.message:'Cannot save your preference.')}finally{setBusy(false)}
 }
 return <><button disabled={busy} className="personal-save-button" onClick={()=>void update('watchlist',!watchlist)}><SavedIcon/>{watchlist?'Remove from Watch List':'Add to Watch List'}</button><button disabled={busy} className="personal-rate-button" onClick={()=>dialog.current?.showModal()}>{rating!==null&&<RatingBadge rating={rating}/>}<span>{rating===null?'Rate':ratingNames[rating-1]}</span></button>{error&&<p role="alert">{error}</p>}<dialog ref={dialog} className="rating-dialog"><h2>Rate {title.name}</h2><div className="rating-options">{ratingNames.map((name,i)=><button key={name} disabled={busy} aria-pressed={rating===i+1} onClick={()=>void update('rating',i+1)}><RatingBadge rating={i+1}/>{name}</button>)}<button disabled={busy||rating===null} onClick={()=>void update('rating',null)}>Clear rating</button><button disabled={busy} onClick={()=>dialog.current?.close()}>Cancel</button></div>{error&&<p role="alert">{error}</p>}</dialog></>;
}
function CollectionGrid({list,kind,expired}:{list:string;kind:string;expired:()=>void}){
 const [titles,setTitles]=useState<MediaTitle[]>([]),[next,setNext]=useState(''),[busy,setBusy]=useState(true),[error,setError]=useState(''),[epoch,setEpoch]=useState(0);
 const controller=useRef<AbortController|null>(null);
 useEffect(()=>{const changed=()=>setEpoch(v=>v+1);window.addEventListener('ted-preferences-changed',changed);return()=>window.removeEventListener('ted-preferences-changed',changed)},[]);
 useEffect(()=>{const c=new AbortController();controller.current=c;setTitles([]);void load('',c.signal);return()=>c.abort()},[list,kind,epoch]);
 async function load(after:string,signal:AbortSignal){setBusy(true);setError('');try{const page=await collection(list,kind,after,signal);if(!signal.aborted){setTitles(old=>after?[...old,...page.titles.filter(t=>!old.some(p=>p.id===t.id&&p.type===t.type))]:page.titles);setNext(page.next)}}catch(e){if(!signal.aborted){if(e instanceof ApiError&&e.status===401)expired();else setError(e instanceof Error?e.message:'Cannot load titles.')}}finally{if(!signal.aborted)setBusy(false)}}
 return <>{error&&<p role="alert">{error}</p>}{busy&&!titles.length?<p role="status">Loading…</p>:!titles.length&&!error?<p className="hint">No titles here yet.</p>:<MediaGrid titles={titles}/ >}{next&&<button disabled={busy} onClick={()=>void load(next,controller.current!.signal)}>{busy?'Loading…':'Load more'}</button>}{error&&<button onClick={()=>setEpoch(v=>v+1)}>Retry</button>}</>;
}
export function WatchList({expired}:{expired:()=>void}){const [kind,setKind]=useState('movie');return <section><h1>Watch List</h1><label className="collection-type">Type <select value={kind} onChange={e=>setKind(e.target.value)}><option value="movie">Movies</option><option value="series">Series</option></select></label><CollectionGrid list="watchlist" kind={kind} expired={expired}/></section>}
export function Ratings({expired}:{expired:()=>void}){return <section><h1>Ratings</h1>{[7,6,5,4,3,2,1,0].map(r=><details className="rating-section" key={r} open><summary>{r>0&&<RatingBadge rating={r}/>}<span>{r===0?'Unrated':ratingNames[r-1]}</span></summary><CollectionGrid list={`ratings/${r}`} kind="" expired={expired}/></details>)}</section>}
