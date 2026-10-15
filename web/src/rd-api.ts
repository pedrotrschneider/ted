import { ApiError, request } from './api.ts';
export interface Torrent {
  id: string; name: string; bytes: number; status: string; progress: number; added: string;
}
export interface TorrentPage { torrents: Torrent[]; page: number; total?: number; next_page?: number }
export interface TorrentFile { id: number; path: string; bytes: number; selected: boolean; video: boolean; suggested_exclusion: boolean }
export interface TorrentDetail extends Torrent { hash: string; files: TorrentFile[] }
export interface RDCacheState {configured:boolean;ready:boolean;total:number;files_pending:number;phase:string;checked_at:string|null;full_rebuilt_at:string|null;error_code:string}
const object = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null;
const integer = (value: unknown): value is number => Number.isSafeInteger(value) && Number(value) >= 0;
const invalid = () => new ApiError('TED returned incomplete source information. Refresh to try again.');
function torrent(value: unknown): Torrent {
  if (!object(value) || typeof value.id !== 'string' || !/^[a-zA-Z0-9_-]{1,128}$/.test(value.id) || typeof value.name !== 'string' || !value.name || !integer(value.bytes) || typeof value.status !== 'string' || !value.status || typeof value.progress !== 'number' || value.progress < 0 || value.progress > 100 || typeof value.added !== 'string' || !Number.isFinite(Date.parse(value.added))) throw invalid();
  return value as unknown as Torrent;
}
function cacheState(v:unknown):RDCacheState{
 if(!object(v)||typeof v.configured!=='boolean'||typeof v.ready!=='boolean'||!integer(v.total)||!integer(v.files_pending)||typeof v.phase!=='string'||!['unconfigured','new','queued','syncing','files','ready','failed'].includes(v.phase)||typeof v.error_code!=='string')throw invalid();
 for(const key of ['checked_at','full_rebuilt_at'])if(v[key]!==null&&(typeof v[key]!=='string'||!Number.isFinite(Date.parse(v[key] as string))))throw invalid();
 return v as unknown as RDCacheState;
}
export const rdApi = {
  cache:async(signal?:AbortSignal):Promise<RDCacheState>=>cacheState(await request('/api/admin/rd/cache','GET',undefined,signal)),
  refresh:async(full=false,signal?:AbortSignal):Promise<RDCacheState>=>cacheState(await request('/api/admin/rd/cache/refresh','POST',{full},signal)),
  status: async (signal?: AbortSignal): Promise<boolean> => {
    const value = await request('/api/admin/rd/status', 'GET', undefined, signal);
    if (!object(value) || typeof value.configured !== 'boolean') throw invalid();
    return value.configured;
  },
  list: async (page = 1, signal?: AbortSignal): Promise<TorrentPage> => {
    const value = await request(`/api/admin/rd/torrents?page=${page}`, 'GET', undefined, signal);
    if (!object(value) || value.page !== page || !Array.isArray(value.torrents) || (value.total !== undefined && !integer(value.total)) || (value.next_page !== undefined && value.next_page !== page + 1)) throw invalid();
    const torrents = value.torrents.map(torrent);
    if (new Set(torrents.map(t => t.id)).size !== torrents.length) throw invalid();
    return { torrents, page, total: value.total as number | undefined, next_page: value.next_page as number | undefined };
  },
  detail: async (id: string, signal?: AbortSignal): Promise<TorrentDetail> => {
    const value = await request(`/api/admin/rd/torrents/${encodeURIComponent(id)}`, 'GET', undefined, signal);
    const info = torrent(value);
    if (!object(value) || info.id !== id || typeof value.hash !== 'string' || !/^[a-f0-9]{40}$/.test(value.hash) || !Array.isArray(value.files)) throw invalid();
    const files = value.files.map((file: unknown) => {
      if (!object(file) || !integer(file.id) || file.id === 0 || typeof file.path !== 'string' || !file.path.startsWith('/') || !integer(file.bytes) || typeof file.selected !== 'boolean' || typeof file.video !== 'boolean' || typeof file.suggested_exclusion !== 'boolean') throw invalid();
      return file as unknown as TorrentFile;
    });
    if (new Set(files.map(f => f.id)).size !== files.length) throw invalid();
    return { ...info, hash: value.hash, files };
  },
};
// Reject unstable pagination without replacing the last good visible result.
export function appendPage(previous: TorrentPage | null, incoming: TorrentPage): TorrentPage {
  if (incoming.page === 1) return incoming;
  if (!previous || previous.next_page !== incoming.page || previous.total !== incoming.total || incoming.torrents.some(item => previous.torrents.some(old => old.id === item.id))) throw new ApiError('The RD list changed while loading. Refresh the list to start again.');
  const torrents = [...previous.torrents, ...incoming.torrents];
  if (incoming.total !== undefined && (torrents.length > incoming.total || (incoming.next_page === undefined && torrents.length !== incoming.total))) throw new ApiError('The RD list is incomplete. Refresh to start again.');
  return { ...incoming, torrents };
}
export function visibleTorrents(items: Torrent[], search: string, sort: string): Torrent[] {
  const filtered = items.filter(item => item.name.toLowerCase().includes(search.trim().toLowerCase()));
  const compare = sort === 'name' ? (a: Torrent, b: Torrent) => a.name.localeCompare(b.name)
    : sort === 'size' ? (a: Torrent, b: Torrent) => b.bytes - a.bytes
    : sort === 'status' ? (a: Torrent, b: Torrent) => a.status.localeCompare(b.status)
    : (a: Torrent, b: Torrent) => Date.parse(b.added) - Date.parse(a.added);
  return filtered.sort((a, b) => compare(a, b) || a.id.localeCompare(b.id));
}
