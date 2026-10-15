import { useEffect, useRef, useState } from 'react';
import { ApiError, errorMessage } from './api';
import { rdApi } from './rd-api';
import type { Torrent, TorrentDetail } from './rd-api';
import type { Series, SeriesMappings } from './series-api';
import { orderedEpisodes } from './episode-matching';
import { saveEpisodeDraft } from './save-episode-draft';
import { draftMappingStatus } from './episode-draft';
import type { EpisodeDraft } from './episode-draft';
import { Sources } from './Sources';
import { EpisodeMappingEditor } from './EpisodeMappingEditor';

export function SeriesMappingWorkspace({show,existing,draft,setDraft,category,setCategory,expired,onSaveFinished,onBusy}:{show:Series;existing:SeriesMappings;draft:EpisodeDraft;setDraft:(draft:EpisodeDraft)=>void;category:SeriesMappings['category'];setCategory:(category:'shows'|'anime')=>void;expired:()=>void;onSaveFinished:(accepted:boolean)=>void;onBusy:(busy:boolean)=>void}) {
 const [selected,setSelected]=useState<Torrent|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState(''),[message,setMessage]=useState('');
 const count=Object.keys(draft).length;
 async function save(){
  const library=existing.category||category;if(!library||!count||busy)return;
  setBusy(true);onBusy(true);setError('');setMessage('');
  let accepted=false;
  try{
   await saveEpisodeDraft(show.id,library,draft);
   accepted=true;
  }catch(e){if(e instanceof ApiError&&e.status===401)expired();else setError(`${errorMessage(e)} Draft selections are kept. Check the operation status before retrying.`)}
  finally{
   setBusy(false);onBusy(false);onSaveFinished(accepted);
  }
 }
 return <fieldset disabled={busy} className="mapping-workspace"><Sources expired={expired} onSelectTorrent={setSelected} reviewPanel={<section className="card draft-review"><h2>Review episode mappings</h2><fieldset className="mapping-choice"><legend>Library category</legend><div className="mapping-radios">{(['shows','anime'] as const).map(value=><label className="check" key={value}><input type="radio" name="mapping-library" value={value} checked={(existing.category||category)===value} disabled={!!existing.category} onChange={()=>setCategory(value)}/>TED {value==='shows'?'Shows':'Anime'}</label>)}</div>{existing.category&&<p className="hint">This series already belongs to this library.</p>}</fieldset><p>{count} unsaved assignments. Select a torrent to add or revise them.</p><p className="hint">Large submissions are processed in atomic batches. Completed batches stay saved if a later batch fails; you can retry unfinished work.</p><p className="hint">Closing the modal or switching torrents keeps this draft. Reloading or leaving this series clears unsaved changes.</p>
 {error&&<p className="notice error" role="alert">{error}</p>}{message&&<p className="notice" role="status">{message}</p>}
 <ul className="draft-cards">{orderedEpisodes(show.episodes).map(episode=>{
  const choice=draft[episode.id],saved=existing.mappings.find(m=>m.episode_id===episode.id),mapped=!!(choice||saved);
  const status=draftMappingStatus(choice,saved);
  const torrent=choice?.source.name??(saved?saved.source_name:''),path=choice?.file.path??saved?.path??'',bytes=choice?.file.bytes??saved?.bytes??0;
  return <li key={episode.id} className={`is-${status}`}><strong>Season {episode.season} Episode {episode.number}<span className="sr-only">{status==='unchanged'?' — unchanged mapping':status==='modified'?' — modified mapping':' — unmapped'}</span></strong>{mapped&&<><span title={torrent}>{torrent}</span><span title={path}>{path.replace(/^\//,'')}</span><small>{(bytes/1048576).toFixed(1)} MiB · {choice?(choice.file.selected?'Selected in RD':'Not selected in RD'):'Saved mapping'}</small></>}</li>
 })}</ul><div className="actions"><button className="primary" disabled={!count||!(existing.category||category)||busy} onClick={()=>void save()}>{busy?'Saving…':'Save mappings'}</button><button disabled={!count||busy} onClick={()=>{setDraft({});setMessage('Draft cleared. Saved mappings kept.')}}>Clear mapping</button></div>
 </section>}/>{selected&&<SourceMappingModal torrent={selected} show={show} existing={existing} draft={draft} setDraft={setDraft} expired={expired} close={()=>setSelected(null)}/>}</fieldset>;
}
function SourceMappingModal({torrent,show,existing,draft,setDraft,expired,close}:{torrent:Torrent;show:Series;existing:SeriesMappings;draft:EpisodeDraft;setDraft:(draft:EpisodeDraft)=>void;expired:()=>void;close:()=>void}) {
 const dialog=useRef<HTMLDialogElement>(null),[source,setSource]=useState<TorrentDetail|null>(null),[error,setError]=useState(''),[revision,setRevision]=useState(0);
 useEffect(()=>{const node=dialog.current!;node.showModal();const previous=document.body.style.overflow;document.body.style.overflow='hidden';return()=>{node.close();document.body.style.overflow=previous}},[]);
 useEffect(()=>{const c=new AbortController();setSource(null);setError('');rdApi.detail(torrent.id,c.signal).then(detail=>{if(!c.signal.aborted)setSource(detail)}).catch(e=>{if(!c.signal.aborted){if(e instanceof ApiError&&e.status===401)expired();else setError(errorMessage(e))}});return()=>c.abort()},[torrent.id,revision]);
 return <dialog ref={dialog} className="mapping-dialog" aria-labelledby="mapping-dialog-title" onCancel={e=>{e.preventDefault();close()}}><div className="mapping-dialog-header"><h2 id="mapping-dialog-title">Map episode files</h2><div className="section-title"><h2 className="mapping-files-heading">Video files</h2><button autoFocus onClick={close}>Close</button></div></div><div className="mapping-dialog-body">{error&&<p role="alert" className="notice error mapping-placeholder">{error}</p>}{source?<EpisodeMappingEditor show={show} source={source} existing={existing} allDraft={draft} changeDraft={setDraft}/>:!error&&<p className="mapping-placeholder" role="status">Reading source files…</p>}</div><div className="actions source-refresh"><button onClick={()=>setRevision(r=>r+1)}>Refresh source files</button><button onClick={close}>Done</button></div></dialog>;
}
