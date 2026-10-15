import {ApiError} from './api';
import {rdApi} from './rd-api';

export async function refreshRDCache(signal:AbortSignal):Promise<void>{
 const initial=await rdApi.refresh(false,signal);
 if(initial.ready&&initial.phase!=='queued'&&initial.phase!=='syncing')return;
 const deadline=Date.now()+60000;
 while(Date.now()<deadline){
  await new Promise<void>((resolve,reject)=>{
   const abort=()=>{clearTimeout(timer);reject(new DOMException('Aborted','AbortError'))};
   const timer=setTimeout(()=>{signal.removeEventListener('abort',abort);resolve()},500);
   if(signal.aborted)abort();else signal.addEventListener('abort',abort,{once:true});
  });
  const v=await rdApi.cache(signal);
  if(v.ready&&v.checked_at!==initial.checked_at)return;
  if(v.phase==='failed')throw new ApiError('Real-Debrid cache refresh failed. Previous results remain available.');
 }
 throw new ApiError('Real-Debrid cache refresh is still running. Check Server providers for progress and retry shortly.');
}
