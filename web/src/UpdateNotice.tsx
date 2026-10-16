import {useEffect, useState} from 'react';
import {request} from './api';

export function UpdateNotice() {
  const [version,setVersion]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState('');
  useEffect(()=>{
    const controller=new AbortController();let generation=0;
    async function check(){
      const current=generation;
      try {
        const value=await request('/api/admin/updates','GET',undefined,controller.signal);
        if(!controller.signal.aborted && current===generation && value && typeof value==='object' && 'version' in value && typeof value.version==='string') setVersion(value.version);
      } catch { /* Update discovery must not interrupt normal use. Try again next hour. */ }
    }
    void check();const timer=setInterval(()=>void check(),60*60*1000);
    // A dismissal invalidates any check already in flight in this tab.
    const onDismiss=()=>{generation++;setVersion('');};
    window.addEventListener('ted-update-dismissed',onDismiss);
    return ()=>{controller.abort();clearInterval(timer);window.removeEventListener('ted-update-dismissed',onDismiss);};
  },[]);
  async function dismiss(){
    if(busy)return;setBusy(true);setError('');
    try {await request('/api/admin/updates/dismiss','POST',{});setVersion('');window.dispatchEvent(new Event('ted-update-dismissed'));}
    catch {setError('Could not save dismissal. Please try again.');}
    finally {setBusy(false);}
  }
  if(!version)return null;
  return <aside className="update-notice" aria-label="TED update available"><div role="status"><strong>TED {version} is available</strong><p>Update your server when you’re ready.</p>{error&&<p role="alert">{error}</p>}</div><button disabled={busy} onClick={()=>void dismiss()} aria-label="Dismiss update notifications for one day">Dismiss for a day</button></aside>;
}
