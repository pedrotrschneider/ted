import {useLayoutEffect,useRef} from 'react';
import {createRouteScroll} from './route-scroll';
export function useRouteScroll(hash:string){
 const scroll=useRef<ReturnType<typeof createRouteScroll>|null>(null);
 useLayoutEffect(()=>{
  scroll.current=createRouteScroll(window.history,()=>{
   document.getElementById('main')?.focus({preventScroll:true});
   window.scrollTo({left:0,top:0,behavior:'instant'});
  });
  return()=>{scroll.current?.dispose();scroll.current=null};
 },[]);
 // Run after React commits the new route, before the browser paints it.
 useLayoutEffect(()=>{scroll.current?.commit(hash)},[hash]);
}
