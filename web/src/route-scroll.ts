// Hash routes replace the page body asynchronously. Browser restoration cannot
// safely restore an old offset against the temporary loading-screen height.
export function createRouteScroll(history:Pick<History,'scrollRestoration'>,reset:()=>void){
 const previous=history.scrollRestoration;
 history.scrollRestoration='manual';
 let route:string|undefined;
 return {
  commit(next:string){if(next!==route){route=next;reset()}},
  dispose(){history.scrollRestoration=previous}
 };
}
