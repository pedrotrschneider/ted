import {useEffect,useRef,useState} from 'react';
import {ApiError,errorMessage} from './api';
import {artworkApi} from './artwork-api';
import type {Artwork,ArtworkChoices,ArtworkPage} from './artwork-api';

const artworkLabel=(a:Artwork)=>a.type==='Primary'?(a.kind==='season'?'Season poster':'Primary (poster)'):a.type;

export function ArtworkEditor({kind,id,name,expired,changed}:{kind:'movie'|'series';id:string;name:string;expired:()=>void;changed:()=>void}){
 const [open,setOpen]=useState(false);
 return <><button type="button" onClick={()=>setOpen(true)}>Edit artwork</button>{open&&<ArtworkDialog kind={kind} id={id} name={name} expired={expired} changed={changed} close={()=>setOpen(false)}/>}</>;
}
function ArtworkDialog({kind,id,name,expired,changed,close}:{kind:'movie'|'series';id:string;name:string;expired:()=>void;changed:()=>void;close:()=>void}){
 const dialog=useRef<HTMLDialogElement>(null),[page,setPage]=useState<ArtworkPage|null>(null),[selected,setSelected]=useState<Artwork|null>(null);
 const [choices,setChoices]=useState<ArtworkChoices|null>(null),[mode,setMode]=useState<'menu'|'provider'>('menu');
 const [error,setError]=useState(''),[busy,setBusy]=useState(false),[revision,setRevision]=useState(0),[choiceRevision,setChoiceRevision]=useState(0),[notice,setNotice]=useState('');
 const lastArtwork=useRef<string|null>(null);
 useEffect(()=>{
  if(!page?.metadata_cached)return;
  const signature=page.images.map(a=>`${a.id}:${a.tag||''}`).join('|');
  if(lastArtwork.current!==null&&lastArtwork.current!==signature)changed();
  lastArtwork.current=signature;
 },[page]);
 useEffect(()=>{const node=dialog.current!;node.showModal();const old=document.body.style.overflow;document.body.style.overflow='hidden';return()=>{node.close();document.body.style.overflow=old}},[]);
 function failure(e:unknown){if(e instanceof ApiError&&e.status===401)expired();else setError(errorMessage(e))}
 useEffect(()=>{
  const c=new AbortController();let timer:ReturnType<typeof setTimeout>;
  async function load(retry=false){try{const v=await artworkApi.list(kind,id,retry,c.signal);if(c.signal.aborted)return;setPage(v);if(v.metadata_pending||v.images.some(a=>a.status==='queued'||a.status==='new'&&a.has_source))timer=setTimeout(()=>void load(),1000)}catch(e){if(!c.signal.aborted)failure(e)}}
  void load(true);return()=>{c.abort();clearTimeout(timer)};
 },[kind,id,revision]);
 useEffect(()=>{
  if(!selected||mode!=='provider')return;
  const c=new AbortController();let timer:ReturnType<typeof setTimeout>;
  setChoices(null);
  async function load(refresh=false){try{const v=await artworkApi.choices(selected!.entity_id,refresh,c.signal);if(c.signal.aborted)return;setChoices(v);if(v.status==='queued'||v.status==='new')timer=setTimeout(()=>void load(),1000)}catch(e){if(!c.signal.aborted)failure(e)}}
  void load(choiceRevision>0);return()=>{c.abort();clearTimeout(timer)};
 },[selected?.entity_id,mode,choiceRevision]);
 async function save(operation:()=>Promise<'queued'|'saved'>){setBusy(true);setError('');try{const status=await operation();setSelected(null);setMode('menu');setNotice(status==='queued'?'Downloading the selected artwork. The previous image remains available until it is ready.':'Artwork saved. Teddy will use it after Reload.');setRevision(n=>n+1);if(status==='saved')changed()}catch(e){failure(e);setRevision(n=>n+1)}finally{setBusy(false)}}
 function back(){setSelected(null);setChoices(null);setMode('menu');setError('');setChoiceRevision(0)}
 const available=choices?.images.filter(c=>c.type===selected?.type)??[];
 return <dialog ref={dialog} className="artwork-dialog" aria-labelledby="artwork-title" onCancel={e=>{e.preventDefault();if(!busy){if(selected)back();else close()}}}>
  <div className="section-title"><h2 id="artwork-title">{selected?`${selected.name||name} · ${artworkLabel(selected)}`:`Artwork · ${name}`}</h2><div className="actions">{selected&&<button disabled={busy} onClick={back}>← All artwork</button>}<button autoFocus disabled={busy} onClick={close}>Close</button></div></div>
  <p className="hint">Artwork is shared by all TED accounts. Your selections are preserved when metadata is refreshed.</p>
  {error&&<p role="alert" className="notice error">{error}</p>}{notice&&<p role="status" className="notice">{notice}</p>}
  {!page?.metadata_cached?<div className="artwork-preparing">{(!page||page.metadata_pending)?<><progress aria-label="Caching metadata"/><p role="status">Preparing the metadata cache…</p></>:<><p role="alert" className="notice error">Metadata could not be cached ({page.metadata_error||page.metadata_status}). Check the provider credentials and retry.</p><button onClick={()=>{setError('');setRevision(n=>n+1)}}>Retry metadata cache</button></>}</div>:!selected?<><div className="actions"><button onClick={()=>{setError('');setRevision(n=>n+1)}}>Refresh cached artwork</button></div>{!page&&<p role="status">Loading cached artwork…</p>}{page?.metadata_pending&&<p role="status">Fetching metadata in the background…</p>}<ul className="artwork-grid">{page?.images.map(a=><li key={a.id}><button className="artwork-card" onClick={()=>{setSelected(a);setError('');setNotice('');setChoiceRevision(0)}}><ArtworkPreview artwork={a}/><strong>{a.name||name}</strong><span>{artworkLabel(a)}</span><small>{a.user_modified?'Custom artwork':a.tag?'Cached':a.status==='queued'?'Downloading…':'Not cached yet'}</small>{a.error_code&&<small className="error">Previous image kept · retry by selecting an image</small>}</button></li>)}</ul></>:
   <><div className="actions artwork-options"><button disabled={busy} onClick={()=>{setMode('provider');setError('')}}>See available on {selected.provider==='Tmdb'?'TMDb':'TVDB'}</button><label className="button file-choice">Choose file<input type="file" accept="image/jpeg,image/png,image/gif" disabled={busy} onChange={event=>{const file=event.currentTarget.files?.[0];event.currentTarget.value='';if(file)void save(()=>artworkApi.upload(selected,file))}}/></label></div><p className="hint">JPEG, PNG or GIF · up to 20 MiB. The previous image stays available until its replacement is verified.</p>
    {mode==='provider'&&<><div className="actions"><button disabled={busy||choices?.status==='queued'} onClick={()=>{setError('');setChoiceRevision(n=>n+1)}}>Refresh provider artwork</button></div>{(!choices||choices.status==='queued'||choices.status==='new')&&<p role="status">Loading available artwork…</p>}{choices?.status==='failed'&&<p role="alert" className="notice error">Provider artwork is unavailable ({choices.error_code}). Retry using Refresh provider artwork.</p>}{choices?.status==='ready'&&!available.length&&<p>No available artwork for this image type. You can choose a file.</p>}<ul className="artwork-grid">{available.map(c=><li key={c.id}><button className="artwork-card" disabled={busy} onClick={()=>void save(()=>artworkApi.select(selected,c))}><img src={c.preview} alt="" loading="lazy" referrerPolicy="no-referrer"/><span>{c.language||'No text / unspecified language'}</span>{c.width>0&&<small>{c.width} × {c.height}</small>}</button></li>)}</ul></>}
   </>}
  {busy&&<p role="status">Saving artwork…</p>}
 </dialog>;
}
function ArtworkPreview({artwork:a}:{artwork:Artwork}){
 const [failed,setFailed]=useState(false);
 useEffect(()=>setFailed(false),[a.tag,a.status]);
 return (a.tag||a.has_source)&&!failed?<img src={`${a.url}?v=${a.tag||a.generation}`} alt="" loading="lazy" onError={()=>setFailed(true)}/>:<span className="artwork-empty" aria-hidden="true">{a.type}</span>;
}
