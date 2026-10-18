import {useWatchTitle,WatchToggle} from './WatchToggle';
import { useEffect, useLayoutEffect, useReducer, useRef, useState } from 'react';
import { ApiError, errorMessage } from './api';
import { seriesApi } from './series-api';
import { seriesDetailState } from './series-detail-state';
import { seasonSteps } from './season-step';
import type { SeriesMappings } from './series-api';
import { MediaHeader } from './MediaPresentation';
import { SeriesMappingWorkspace } from './SeriesMappingWorkspace';
import type { EpisodeDraft } from './episode-draft';
import { MappingProgress } from './MappingProgress';
import { EpisodeRow } from './EpisodeRow';
import { BatchDownload } from './BatchDownload';
import { RemoveLibrary } from './RemoveLibrary';
import { ArtworkEditor } from './ArtworkEditor';

export function SeriesDetail({id,admin,expired}:{id:string;admin:boolean;expired:()=>void}){
 const watch=useWatchTitle(id,expired);
 const[{series,mapping,error},dispatch]=useReducer(seriesDetailState,{id,admin,series:null,mapping:null,error:''});
 const[revision,setRevision]=useState(0),[picking,setPicking]=useState(false);
 useEffect(()=>{const c=new AbortController();dispatch({type:'refresh',id,admin});Promise.all([seriesApi.detail(id,c.signal),admin?seriesApi.mappings(id,c.signal):Promise.resolve(null)]).then(([series,mapping])=>{if(!c.signal.aborted)dispatch({type:'loaded',id,admin,series,mapping})}).catch(e=>{if(!c.signal.aborted){if(e instanceof ApiError&&e.status===401)expired();else dispatch({type:'failed',id,admin,error:errorMessage(e)})}});return()=>c.abort()},[id,revision,admin]);
 const [draft,setDraft]=useState<EpisodeDraft>({}),[category,setCategory]=useState<SeriesMappings['category']>('');
 const [mappingBusy,setMappingBusy]=useState(false),[preparationRevision,setPreparationRevision]=useState(0);
 const [removalBusy,setRemovalBusy]=useState(false);
 const [downloadRevision,setDownloadRevision]=useState(0);
 const [operationBusy,setOperationBusy]=useState(false);
 const [collapsedSeasons,setCollapsedSeasons]=useState<Set<number>>(()=>new Set());
 function toggleSeason(season:number){setCollapsedSeasons(previous=>{const next=new Set(previous);if(next.has(season))next.delete(season);else next.add(season);return next})}
 const pickerScroll=useRef<{left:number;top:number}|null>(null);
 useLayoutEffect(()=>{if(pickerScroll.current){window.scrollTo({...pickerScroll.current,behavior:'instant'});pickerScroll.current=null}},[picking]);
 function togglePicker(){pickerScroll.current={left:window.scrollX,top:window.scrollY};setPicking(p=>!p)}
 const seasons=seasonSteps(series?.episodes??[]);
 return <div className="series-page"><div className="section-title"><a href="#series">← Back to series</a><button disabled={mappingBusy||operationBusy||removalBusy} onClick={()=>setRevision(v=>v+1)}>Refresh details and mappings</button></div>{error&&<p className="notice error" role="alert">{error}</p>}
 {series?<><MediaHeader title={series} watch={watch}/>{watch.error&&<p role="alert" className="notice error">{watch.error}</p>}{mapping?.category&&<p>Library category: TED {mapping.category==='anime'?'Anime':'Series'}</p>}
 {admin&&mapping&&<><p>{mapping.mappings.length} of {series.episodes.length} catalog episodes mapped. Missing episodes can be added from another source.</p><div className="panel-stack"><MappingProgress kind="series" id={id} revision={preparationRevision} expired={expired} busy={setOperationBusy} batchCompleted={()=>setRevision(n=>n+1)} completed={()=>{setDraft({});setRevision(n=>n+1)}}/><div className="actions"><button disabled={mappingBusy||operationBusy||removalBusy} onClick={togglePicker}>{picking?'Close source picker':'Map episodes from an RD source'}</button><ArtworkEditor kind="series" id={id} name={series.name} expired={expired} changed={()=>setRevision(n=>n+1)}/></div>{picking&&<fieldset className="mapping-workspace" disabled={operationBusy||removalBusy}><SeriesMappingWorkspace series={series} existing={mapping} draft={draft} setDraft={setDraft} category={category} setCategory={setCategory} expired={expired} onSaveFinished={accepted=>{if(accepted){pickerScroll.current={left:window.scrollX,top:window.scrollY};setPicking(false);setOperationBusy(true)}setPreparationRevision(n=>n+1)}} onBusy={setMappingBusy}/></fieldset>}</div></>}
 <div className="season-panels"><BatchDownload id={id} disabled={operationBusy||mappingBusy||removalBusy} expired={expired} queued={()=>setDownloadRevision(n=>n+1)}/></div>
 <div className="panel-stack season-panels">{seasons.map(season=><section className="card" key={season}><div className="section-title"><div className="season-heading"><WatchToggle control={watch} season={season} label={season===0?'Specials':`Season ${season}`}/><h2><button type="button" className="season-toggle" aria-expanded={!collapsedSeasons.has(season)} aria-controls={`season-${season}-episodes`} onClick={()=>toggleSeason(season)}><span aria-hidden="true">{collapsedSeasons.has(season)?'▸':'▾'}</span>{season===0?'Specials':`Season ${season}`}</button></h2></div><BatchDownload id={id} season={season} disabled={operationBusy||mappingBusy||removalBusy} expired={expired} queued={()=>setDownloadRevision(n=>n+1)}/></div><ul className="episode-list" id={`season-${season}-episodes`} hidden={collapsedSeasons.has(season)}>{series.episodes.filter(e=>e.season===season).map(e=><EpisodeRow watch={watch} key={e.id} episode={e} mapped={mapping?.mappings.some(m=>m.episode_id===e.id)} admin={admin} expired={expired} revision={revision+preparationRevision+downloadRevision}/>)}</ul></section>)}</div>
 {admin&&<RemoveLibrary kind="series" id={id} name={series.name} present={!!mapping?.mappings.length} disabled={operationBusy||mappingBusy} expired={expired} busy={setRemovalBusy} completed={()=>{setDraft({});setPicking(false);setRevision(n=>n+1)}}/>}
 {series.episodes.length===0&&<p>Cinemeta currently lists no episodes for this series.</p>}</>:!error&&<p>Loading series…</p>}</div>;
}
