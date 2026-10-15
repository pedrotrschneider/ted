import {useWatchTitle} from './WatchToggle';
import { useEffect, useState } from 'react';
import { ApiError, errorMessage } from './api';
import { catalogApi } from './catalog-api';
import type { Mapping, Movie } from './catalog-api';
import { MediaHeader } from './MediaPresentation';
import { Sources } from './Sources';
import { MappingProgress } from './MappingProgress';
import { LocalDownload } from './LocalDownload';
import { RemoveLibrary } from './RemoveLibrary';
import { ArtworkEditor } from './ArtworkEditor';

export function MovieDetail({ id, admin, expired }: { id: string; admin: boolean; expired: () => void }) {
 const watch=useWatchTitle(id,expired);
  const [movie, setMovie] = useState<Movie | null>(null), [mapping, setMapping] = useState<Mapping | null>(null);
  const [loading, setLoading] = useState(true), [error, setError] = useState(''), [mappingKnown, setMappingKnown] = useState(false), [attempt, setAttempt] = useState(0), [picking, setPicking] = useState(false);
  const [removalBusy,setRemovalBusy]=useState(false);
  const [operationRevision,setOperationRevision]=useState(0),[operationBusy,setOperationBusy]=useState(false);
  useEffect(() => {
    const controller = new AbortController(); setLoading(true); setError(''); setMappingKnown(false);
    Promise.allSettled([catalogApi.movie(id, controller.signal), admin ? catalogApi.mapping(id, controller.signal) : Promise.resolve(null)]).then(([detail, saved]) => {
      if (controller.signal.aborted) return;
      const errors: string[] = [];
      for (const result of [detail, saved]) if (result.status === 'rejected') { if (result.reason instanceof ApiError && result.reason.status === 401) { expired(); return; } errors.push(errorMessage(result.reason)); }
      if (saved.status === 'fulfilled') { setMapping(saved.value); setMappingKnown(true); }
      if (detail.status === 'fulfilled') setMovie(detail.value);
      else if (saved.status === 'fulfilled' && saved.value) { setMovie(saved.value.movie); errors.push('Showing the saved movie details.'); }
      setError(errors.join(' ')); setLoading(false);
    });
    return () => controller.abort();
  }, [id, attempt, admin]);
  return <>
    <div className="section-title"><a href="#movies">← Back to movies</a><button disabled={loading} onClick={() => setAttempt(attempt + 1)}>Refresh details and mapping</button></div>
    {loading && <p role="status">Loading movie…</p>}{error && <p className="notice error" role="alert">{error}</p>}
    {movie && <><MediaHeader title={movie} watch={watch}/>{watch.error&&<p role="alert" className="notice error">{watch.error}</p>}<div className="panel-stack">
      {mapping&&<section className="card"><h2>Current mapping</h2><p className="source-name"><strong>{mapping.source_name}</strong></p><p className="source-name">{mapping.path.replace(/^\//, '')}</p><p>{mapping.bytes.toLocaleString()} bytes</p></section>}
      {admin&&<><MappingProgress kind="movies" id={id} revision={operationRevision} expired={expired} busy={setOperationBusy} completed={()=>{setAttempt(n=>n+1);setPicking(false)}}/><p className="notice">Saving validates the file before replacing the current mapping. Replacing a source invalidates its local copy and may interrupt playback.</p><div className="actions"><button disabled={loading||!mappingKnown||operationBusy||removalBusy} onClick={()=>setPicking(!picking)}>{picking?'Close source picker':mapping?'Replace Real-Debrid source':'Choose a Real-Debrid source'}</button><ArtworkEditor kind="movie" id={id} name={movie.name} expired={expired} changed={()=>setAttempt(n=>n+1)}/></div>{picking&&mappingKnown&&!loading&&<fieldset className="mapping-workspace" disabled={operationBusy||removalBusy}><Sources expired={expired} target={{name:movie.name,save:async(source,file,signal)=>{await catalogApi.save(id,source,file,signal);if(!signal.aborted){setPicking(false);setOperationBusy(true);setOperationRevision(n=>n+1)}}}}/></fieldset>}</>}
      <LocalDownload id={id} admin={admin} expired={expired}/>{admin&&<RemoveLibrary kind="movies" id={id} name={movie.name} present={!!mapping} disabled={operationBusy} expired={expired} busy={setRemovalBusy} completed={()=>{setPicking(false);setAttempt(n=>n+1)}}/>}</div>
    </>}
  </>;
}
