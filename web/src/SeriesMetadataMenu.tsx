import {useEffect,useRef,useState} from 'react';
import {ApiError,errorMessage} from './api';
import {otherProvider,providerLabel,seriesProviderApi} from './series-provider-api';
import type {SeriesProvider,OverrideAction} from './series-provider-api';

export function SeriesMetadataMenu({id,expired,changed}:{id:string;expired:()=>void;changed:()=>void}){
 const[open,setOpen]=useState(false);
 return <><button type="button" aria-label="Series options" title="Series options" onClick={()=>setOpen(true)}><svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="12" cy="5" r="2"/><circle cx="12" cy="12" r="2"/><circle cx="12" cy="19" r="2"/></svg></button>{open&&<ProviderDialog key={id} id={id} expired={expired} changed={()=>{setOpen(false);changed()}} close={()=>setOpen(false)}/>}</>;
}
function ProviderDialog({id,expired,changed,close}:{id:string;expired:()=>void;changed:()=>void;close:()=>void}){
 const dialog=useRef<HTMLDialogElement>(null),controller=useRef(new AbortController());
 const[provider,setProvider]=useState<SeriesProvider|null>(null),[confirm,setConfirm]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[overrideAction,setOverrideAction]=useState<OverrideAction>('remove');
 useEffect(()=>{const node=dialog.current!;controller.current=new AbortController();const c=controller.current;node.showModal();return()=>{c.abort();node.close()}},[]);
 function failure(e:unknown){if(e instanceof ApiError&&e.status===401)expired();else setError(errorMessage(e))}
 async function choose(){setError('');try{const v=await seriesProviderApi.get(id,controller.current.signal);if(!controller.current.signal.aborted){setProvider(v);setOverrideAction('remove');setConfirm(true)}}catch(e){if(!controller.current.signal.aborted)failure(e)}}
 async function change(){if(!provider||busy)return;setBusy(true);setError('');const signal=controller.current.signal;
  try{await seriesProviderApi.change(id,provider.provider,{action:overrideAction,version:provider.overrides.version},signal);if(!signal.aborted)changed();
  }catch(e){if(!signal.aborted){failure(e);if(e instanceof ApiError&&e.status===409)setConfirm(false)}}finally{if(!signal.aborted)setBusy(false)}
 }
 return <dialog ref={dialog} className="artwork-dialog" aria-label={confirm?'Change metadata provider':'Series options'} onCancel={e=>{e.preventDefault();close()}}>
  <div className="section-title"><h2>{confirm?'Change metadata provider':'Series options'}</h2><button onClick={close}>Close</button></div>
  {error&&<p role="alert" className="notice error">{error}</p>}
  {!confirm?<button autoFocus onClick={()=>void choose()}>Change metadata provider</button>:provider&&<><p>Currently using {providerLabel(provider.provider)}. Change to {providerLabel(otherProvider(provider.provider))}?</p><p className="hint">This changes descriptions, artwork, cast and season/episode organization for everyone on this server. Existing history, downloads and source mappings are preserved.</p>{provider.overrides.artwork+provider.overrides.metadata>0&&<fieldset className="provider-overrides" disabled={busy}><legend>Manual overrides</legend><p>{provider.overrides.artwork} artwork overrides and {provider.overrides.metadata} metadata overrides, including any hidden seasons.</p><label><input type="radio" name="overrides" value="remove" checked={overrideAction==='remove'} onChange={()=>setOverrideAction('remove')}/> Remove overrides and use the new provider</label><label><input type="radio" name="overrides" value="keep" checked={overrideAction==='keep'} onChange={()=>setOverrideAction('keep')}/> Keep my overrides</label></fieldset>}<div className="actions"><button disabled={busy} onClick={()=>setConfirm(false)}>Cancel</button><button disabled={busy||!provider.can_change} onClick={()=>void change()}>Change provider</button></div></>}
  {busy&&<p role="status">Changing metadata provider…</p>}
 </dialog>;
}
