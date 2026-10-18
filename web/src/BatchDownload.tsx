import {useState} from 'react';
import {ApiError,errorMessage} from './api';
import {downloadSeries} from './local-api';
export function BatchDownload({id,season,disabled,expired,queued}:{id:string;season?:number;disabled:boolean;expired:()=>void;queued:()=>void}){
 const[busy,setBusy]=useState(false),[readyOnly,setReadyOnly]=useState(false),[error,setError]=useState(''),[message,setMessage]=useState('');
 async function download(){
  setBusy(true);setError('');setMessage('');
  try{const result=await downloadSeries(id,season,readyOnly);setMessage(`${result.queued} queued · ${result.existing} already downloaded or busy${result.missing?` · ${result.missing} not ready (skipped)`:''}.`)}
  catch(e){if(e instanceof ApiError&&e.status===401)expired();else setError(errorMessage(e))}
  finally{setBusy(false);queued()}
 }
 return <div className="batch-download"><div className="actions"><button disabled={disabled||busy} onClick={()=>void download()}>{busy?'Queueing…':season===undefined?'Download series':'Download season'}</button><label className="check"><input type="checkbox" checked={readyOnly} disabled={disabled||busy} onChange={e=>setReadyOnly(e.target.checked)}/>Ready episodes only</label></div>{message&&<p role="status">{message}</p>}{error&&<p className="notice error" role="alert">{error}</p>}</div>;
}
