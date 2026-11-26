import {createRoot} from 'react-dom/client';
import {ClientLogs} from '../src/ClientLogs';
import '../src/style.css';

declare global {interface Window {__logRequests:{path:string;method:string}[]}}
window.__logRequests=[];
const settings={retention_days:90,stored_bytes:1048576,max_stored_bytes:1073741824,max_device_bytes:67108864};
window.fetch=async(input,init)=>{
 const url=new URL(String(input),location.origin),method=init?.method??'GET';
 window.__logRequests.push({path:url.pathname+url.search,method});
 if(url.pathname==='/api/admin/logs/settings'){
  if(method==='PUT')settings.retention_days=JSON.parse(String(init?.body)).retention_days;
  return Response.json(settings);
 }
 if(url.pathname==='/api/admin/logs')return Response.json({head_seq:3,entries:Array.from({length:3},(_,i)=>({
  id:`entry-${i}`,at:'2026-11-26T12:47:27Z',received_at:'2026-11-26T12:47:30Z',level:i===1?'warning':'info',component:'ted_playback',
  message:i===1?`TED playback: phase=buffering source=${'long_source_name_'.repeat(25)}`:'TED playback: phase=ready elapsed_ms=120',
  user_id:1,username:'demo',device_id:'91bf6997-0ec5-46c1-a932-1d491edcbfed',device_name:'Teddy macos',platform:'macos',app_version:'0.3.18',batch_seq:3-i,
 }))});
 throw Error(`Unexpected fixture request: ${url.pathname}`);
};
const pages=['Movies','Series','Watch List','Ratings','Watch history','Your account','Logs','People','Sources','Stremio account sync'];
createRoot(document.getElementById('root')!).render(<div className="shell">
 <header><a className="brand" href="#"><span className="mark">TED<span>●</span></span><span>The Entertainment<br/>District</span></a><div className="header-account"><span>demo</span><button>Sign out</button></div></header>
 <nav aria-label="Main navigation">{pages.map(page=><a key={page} href="#logs" aria-current={page==='Logs'?'page':undefined}>{page}</a>)}</nav>
 <main><ClientLogs expired={()=>{throw Error('Unexpected fixture expiry');}}/></main>
</div>);
