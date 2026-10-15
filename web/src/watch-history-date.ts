import type {WatchEntry} from './watch-api.ts';

export function historyDate(entry:Pick<WatchEntry,'kind'|'state'|'observed_at'>){
 const received=new Date(entry.observed_at).toLocaleString();
 if(entry.kind==='stremio_import'||entry.kind==='stremio_observed')return `${entry.kind==='stremio_import'?'Imported':'Detected'} ${received} · Viewing date unknown`;
 if(entry.kind==='ted_watched'||entry.kind==='watch_date_corrected')return entry.state.last_played?`Watched ${new Date(entry.state.last_played).toLocaleString()}`:`Viewing date unknown · Recorded ${received}`;
 return `Observed ${received}`;
}
