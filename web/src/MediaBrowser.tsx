import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { ApiError, errorMessage } from './api';
import { mediaBrowserApi } from './media-browser-api';
import type { MediaKind, MediaTitle } from './media-browser-api';
import { discoveryFilters, browseHeading, genres, toggleWatchAccount, watchAccountTextColor } from './discovery-filters';
import type { BrowseSelection, DiscoveryCatalog, WatchFilterAccount } from './discovery-filters';
import { MediaGrid } from './MediaPresentation';
import { MovieDetail } from './MovieDetail';
import { SeriesDetail } from './SeriesDetail';
interface Results {titles:MediaTitle[];next?:number|string;key:string;label:string}
export function MediaBrowser({hash,kind,admin,expired}:{hash:string;kind:MediaKind;admin:boolean;expired:()=>void}) {
 const plural=kind==='movie'?'movies':'series';
 const id=new RegExp(`^#${kind}/(tt[0-9]{7,12})$`).exec(hash)?.[1]??'';
 const [search,setSearch]=useState('');
 const [selection,setSelection]=useState<BrowseSelection>({catalog:'popular',genre:'',year:new Date().getFullYear(),saved:false,watched:false,search:''});
 const [page,setPage]=useState<Results|null>(null),[loading,setLoading]=useState(false),[error,setError]=useState('');
 const [watchAccounts,setWatchAccounts]=useState<WatchFilterAccount[]>([]);
 const read=useRef<AbortController|null>(null),current=useRef<Results|null>(null);
 const key=JSON.stringify([kind,selection]),personal=selection.saved||selection.watched;
 const textSearch=!!selection.search&&!personal,yearFilter=!personal&&!textSearch&&selection.catalog==='new';
 const label=browseHeading(kind,selection);
 async function load(cursor?:number|string) {
  read.current?.abort();const c=new AbortController();read.current=c;setLoading(true);setError('');
  try {
   const next=personal?await mediaBrowserApi.library(kind,selection,typeof cursor==='string'?cursor:'',c.signal):await mediaBrowserApi.browse(kind,selection.search,typeof cursor==='number'?cursor:0,c.signal,discoveryFilters(selection));
   if(c.signal.aborted)return;
   if('watchAccounts' in next)setWatchAccounts(next.watchAccounts);
   const old=current.current;
   if(cursor!==undefined&&(!old||old.key!==key||old.next!==cursor||next.titles.some(t=>old.titles.some(p=>p.id===t.id))))throw new ApiError('Results changed while loading. Refresh to start again.');
   const result={...next,titles:cursor===undefined?next.titles:[...old!.titles,...next.titles],key,label};
   current.current=result;setPage(result);
  }catch(e){if(!c.signal.aborted){if(e instanceof ApiError&&e.status===401)expired();else setError(errorMessage(e))}}
  finally{if(!c.signal.aborted)setLoading(false)}
 }
 useEffect(()=>{void load();return()=>read.current?.abort()},[key]);
 function change(update:Partial<BrowseSelection>){setSelection(old=>({...old,...update}))}
 if(id)return kind==='movie'?<MovieDetail key={id} id={id} admin={admin} expired={expired}/>:<SeriesDetail key={id} id={id} admin={admin} expired={expired}/>;
 const years=Array.from({length:new Date().getFullYear()-(kind==='movie'?1920:1960)+1},(_,i)=>new Date().getFullYear()-i);
 return <>
 <div className="page-heading"><p className="eyebrow">DISCOVER WITH CINEMETA</p><h1>Find your next {kind}</h1></div>
 <form className="movie-search" onSubmit={e=>{e.preventDefault();change({search:search.trim()})}}><label>Search {plural}<input type="search" maxLength={200} value={search} onChange={e=>setSearch(e.target.value)} placeholder={kind==='movie'?'The Dark Knight Rises':'Birdy the Mighty Decode'}/></label><button type="submit">Search</button>{(search||selection.search)&&<button type="button" onClick={()=>{setSearch('');change({search:''})}}>Clear search</button>}</form>
 <div className="discovery-filters">
 <div className="discovery-toggles"><button type="button" aria-pressed={selection.saved} onClick={()=>change({saved:!selection.saved})}>Mapped</button><button type="button" aria-pressed={selection.watched} onClick={()=>change({watched:!selection.watched,watchedAccounts:undefined})}>Watched</button></div>
 <label>Catalog<select value={selection.catalog} disabled={personal||textSearch} onChange={e=>change({catalog:e.target.value as DiscoveryCatalog})}><option value="popular">Popular</option><option value="new">New</option><option value="featured">Featured</option></select></label>
 {yearFilter?<label>Release year<select value={selection.year} onChange={e=>change({year:Number(e.target.value)})}>{years.map(y=><option key={y} value={y}>{y}</option>)}</select></label>:<label>Genre<select value={selection.genre} disabled={textSearch} onChange={e=>change({genre:e.target.value})}>{genres.map(g=><option key={g} value={g==='None'?'':g}>{g}</option>)}</select></label>}
 </div>
 {selection.watched&&<div className="watched-account-filters" role="group" aria-label="Include watched titles from">
 {watchAccounts.map(account=><button type="button" key={account.id} aria-pressed={selection.watchedAccounts===undefined||selection.watchedAccounts.includes(account.id)} style={{'--account-color':account.color,'--account-text':watchAccountTextColor(account.color)} as CSSProperties} onClick={()=>change({watchedAccounts:toggleWatchAccount(selection.watchedAccounts,watchAccounts,account.id)})}>{account.name}</button>)}
 {!watchAccounts.length&&<span role="status">Loading account filters…</span>}
 </div>}
 {textSearch&&<p className="muted">Search covers all Cinemeta titles. Clear search to browse by catalog and genre.</p>}
 {personal&&<p className="muted">{selection.watched?'Showing titles viewed by any selected account, including previous viewing of titles marked unwatched again.':'Showing mapped titles.'} Search and genre filter this list.</p>}
 {error&&<p className="notice error" role="alert">{error}{page?' Previous results remain below.':''}</p>}
 <div className="section-title"><h2>{page?.key===key||!page?label:page.label}</h2><button disabled={loading} onClick={()=>void load()}>Refresh</button></div>
 {loading&&<p role="status">Loading {plural}…</p>}{page?.key===key&&!page.titles.length&&<p>No {plural} match these filters.</p>}
 <MediaGrid titles={page?.titles??[]}/>{page?.key===key&&page.next!==undefined&&<button disabled={loading||!!error} onClick={()=>void load(page.next)}>Load more {plural}</button>}
 </>;
}
