import type { ReactNode } from 'react';
import { useEffect, useRef, useState } from 'react';
import { ApiError, errorMessage } from './api';
import { appendPage, rdApi, visibleTorrents } from './rd-api';
import {refreshRDCache} from './rd-refresh';
import type { Torrent, TorrentDetail, TorrentFile, TorrentPage } from './rd-api';

interface MappingTarget { name: string; save: (source: TorrentDetail, file: TorrentFile, signal: AbortSignal) => Promise<void> }

function size(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let value = bytes / 1024, i = 0;
  while (value >= 1024 && i < units.length - 1) { value /= 1024; i++; }
  return `${value.toFixed(1)} ${units[i]}`;
}
const status = (value: string) => value === 'downloaded' ? 'Ready in RD' : value.replaceAll('_', ' ');
export function Sources({ expired, target, onSelectTorrent, reviewPanel }: { expired: () => void; target?: MappingTarget; onSelectTorrent?: (source:Torrent)=>void; reviewPanel?:ReactNode }) {
  const [configured, setConfigured] = useState<boolean | null>(null);
  const [page, setPage] = useState<TorrentPage | null>(null);
  const [loading, setLoading] = useState(false), [error, setError] = useState('');
  const [search, setSearch] = useState(''), [sort, setSort] = useState('date');
  const [selected, setSelected] = useState<Torrent | null>(null);
  const read = useRef<AbortController | null>(null), currentPage = useRef<TorrentPage | null>(null);
  async function load(number = 1, refresh = false) {
    read.current?.abort();
    const controller = new AbortController(); read.current = controller;
    setLoading(true); setError('');
    try {
      const enabled = await rdApi.status(controller.signal);
      if (controller.signal.aborted) return;
      setConfigured(enabled);
      if (!enabled) return;
      if(refresh)await refreshRDCache(controller.signal);
      const next = await rdApi.list(number, controller.signal);
      if (controller.signal.aborted) return;
      const merged = appendPage(currentPage.current, next);
      currentPage.current = merged; setPage(merged);
    } catch (e) {
      if (!controller.signal.aborted) {
        if (e instanceof ApiError && e.status === 401) expired(); else setError(errorMessage(e));
      }
    } finally { if (!controller.signal.aborted) setLoading(false); }
  }
  useEffect(() => { void load(); return () => read.current?.abort(); }, []);
  const items = visibleTorrents(page?.torrents ?? [], search, sort);
  return <>
    <div className="page-heading"><p className="eyebrow">REAL-DEBRID</p><h2>{target ? `Choose a source for ${target.name}` : 'Your sources'}</h2>
      <p>Browse torrents in the connected RD account and inspect their files.</p>
    </div>
    <p className="notice">Browsing does not change RD selections or download files.{!target && !onSelectTorrent && ' Open a movie in Movies to save a source mapping.'}</p>
    <div className="source-layout">
      <section className="card">
        <div className="section-title"><h2>Account torrents</h2><button disabled={loading} onClick={() => void load(1,true)}>Refresh</button></div>
        {configured === false && <p className="notice">Set <code>TED_REAL_DEBRID_API_KEY</code> in the server’s environment and recreate the app container to connect this account.</p>}
        {error && <p className="notice error" role="alert">{error}</p>}
        <div className="source-tools">
          <label>Find a torrent<input type="search" value={search} onChange={e => setSearch(e.target.value)} placeholder="Search torrent names"/></label>
          <label>Sort by<select value={sort} onChange={e => setSort(e.target.value)}><option value="date">Newest first</option><option value="name">Name</option><option value="size">Largest first</option><option value="status">RD status</option></select></label>
        </div>
        {loading && <p role="status">Reading Real-Debrid…</p>}
        {page && <p className="hint">{page.torrents.length}{page.total === undefined ? '' : ` of ${page.total}`} torrents loaded. {page.next_page ? 'Search and sort apply to loaded pages; load more for the rest.' : 'All pages loaded.'}</p>}
        {!loading && page && items.length === 0 && <p>{search ? 'No matching torrents in the loaded pages.' : 'This account has no torrents.'}</p>}
        <ul className="source-list">{items.map(item => <li key={item.id}>
          <button className="source-item" aria-pressed={selected?.id === item.id} onClick={() => {setSelected(item);onSelectTorrent?.(item)}}>
            <strong>{item.name}</strong>
            <span>{status(item.status)} · {size(item.bytes)} selected in RD · {item.progress}%</span>
            <span>Added {new Date(item.added).toLocaleDateString()}</span>
          </button>
        </li>)}</ul>
        {page?.next_page && <button disabled={loading} onClick={() => void load(page.next_page)}>Load more torrents</button>}
      </section>
      {reviewPanel ?? (selected ? <Files key={selected.id} torrent={selected} expired={expired} target={target}/> : <section className="card source-empty"><h2>Inspect a source</h2><p>Choose a torrent to see its files, sizes, and RD selection state.</p></section>)}
    </div>
  </>;
}
function Files({ torrent, expired, target }: { torrent: Torrent; expired: () => void; target?: MappingTarget }) {
  const [detail, setDetail] = useState<TorrentDetail | null>(null);
  const [error, setError] = useState(''), [loading, setLoading] = useState(true);
  const [attempt, setAttempt] = useState(0), [hideExtras, setHideExtras] = useState(true), [videosOnly, setVideosOnly] = useState(true);
  const [chosen, setChosen] = useState<number | null>(null), [saving, setSaving] = useState(false), [saveError, setSaveError] = useState('');
  const write = useRef<AbortController | null>(null);
  useEffect(() => () => write.current?.abort(), []);
  async function save() {
    const file = detail?.files.find(f => f.id === chosen);
    if (!target || !detail || !file || saving) return;
    const controller = new AbortController(); write.current = controller; setSaving(true); setSaveError('');
    try { await target.save(detail, file, controller.signal); }
    catch (e) { if (!controller.signal.aborted) { if (e instanceof ApiError && e.status === 401) expired(); else setSaveError(errorMessage(e)); } }
    finally { if (!controller.signal.aborted) setSaving(false); }
  }
  useEffect(() => {
    const controller = new AbortController(); setLoading(true); setError(''); setChosen(null);
    rdApi.detail(torrent.id, controller.signal).then(value => { if (!controller.signal.aborted) setDetail(value); }).catch(e => {
      if (!controller.signal.aborted) {
        if (e instanceof ApiError && e.status === 401) expired(); else setError(errorMessage(e));
      }
    }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [torrent.id, attempt]);
  const visible = detail?.files.filter(file => (!hideExtras || !file.suggested_exclusion) && (!videosOnly || file.video)) ?? [];
  return <section className="card source-files">
    <div className="section-title"><h2>Files</h2><button disabled={loading || saving} onClick={() => setAttempt(attempt + 1)}>Refresh files</button></div>
    <p className="source-name">{detail?.name ?? torrent.name}</p>
    {error && <p className="notice error" role="alert">{error}</p>}
    {loading && <p role="status">Reading file details…</p>}
    <label className="check"><input type="checkbox" disabled={saving} checked={hideExtras} onChange={e => { setHideExtras(e.target.checked); setChosen(null); }}/>Hide Extra, Extras and Sample folders</label>
    <label className="check"><input type="checkbox" disabled={saving} checked={videosOnly} onChange={e => { setVideosOnly(e.target.checked); setChosen(null); }}/>Show video files only</label>
    {detail && <>
      <p>{visible.length} of {detail.files.length} files shown · {status(detail.status)}</p>
      <p className="hint">“Selected in RD” describes the existing provider selection. It does not mean a local TED copy exists.</p>
      <ul className="file-list">{visible.map(file => <li key={file.id}>
        <strong>{file.path.replace(/^\//, '')}</strong>
        <span>{size(file.bytes)} · {file.selected ? 'Selected in RD' : 'Not selected in RD'}</span>
        {target && file.video && file.bytes > 0 && <label className="check"><input type="radio" name="movie-file" checked={chosen === file.id} disabled={loading || saving || !!error} onChange={() => { setChosen(file.id); setSaveError(''); }}/>Use this file for {target.name}</label>}
      </li>)}</ul>
      {visible.length === 0 && <p>No files match these filters.</p>}
      {target && chosen !== null && <div className="confirmation"><p>Save this file as <strong>{target.name}</strong>? TED will validate this file before activating the mapping. An existing local copy will be invalidated on success.</p><p className="source-name">{detail.files.find(f => f.id === chosen)?.path}</p>{saveError && <p className="notice error" role="alert">{saveError} Refresh the movie’s mapping to check whether an interrupted save completed.</p>}<button className="primary" disabled={loading || saving || !!error} onClick={() => void save()}>{saving ? 'Saving mapping…' : 'Save mapping'}</button></div>}
    </>}
  </section>;
}
