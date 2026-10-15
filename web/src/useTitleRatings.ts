import {useEffect,useState} from 'react';
import {ratings} from './ratings-api';
import type {MediaTitle} from './media-browser-api';
export function useTitleRatings(titles:MediaTitle[]){
 const key=JSON.stringify(titles.map(t=>[t.type,t.id]));
 const [values,setValues]=useState<Record<string,number|null>>({}),[error,setError]=useState('');
 useEffect(()=>{
  const c=new AbortController();let timer:ReturnType<typeof setTimeout>;
  const pairs=JSON.parse(key) as ['movie'|'series',string][];
  async function refresh(){
   let pending=false;
   try{
    const result=(await Promise.all((['movie','series'] as const).map(kind=>ratings(kind,pairs.filter(p=>p[0]===kind).map(p=>p[1]),c.signal)))).flat();
    pending=result.some(r=>r.pending);
    if(!c.signal.aborted){setValues(Object.fromEntries(result.map(r=>[r.id,r.rating])));setError('')}
   }catch{pending=true;if(!c.signal.aborted)setError('IMDb ratings could not be refreshed.')}
   finally{if(!c.signal.aborted&&pending)timer=setTimeout(()=>void refresh(),5000)}
  }
  if(pairs.length)void refresh();
  return()=>{c.abort();clearTimeout(timer)};
 },[key]);
 return {values,error};
}
