import {discoveryQuery} from './discovery-filters.ts';
import type {DiscoveryFilters} from './discovery-filters.ts';
import {mappingOperation} from './mapping-operation-api.ts';
import type {MappingOperation} from './mapping-operation-api.ts';
import { ApiError, request } from './api.ts';
import {titleArtworkURL} from './artwork-url.ts';
import type { TorrentDetail, TorrentFile } from './rd-api.ts';
export interface Movie { id: string; type: 'movie'; name: string; year: string; description: string; poster: string; genres: string[] }
export interface MoviePage { movies: Movie[]; skip: number; next_skip?: number }
export interface Mapping { id: string; movie_id: string; movie: Movie; rd_account_id: string; torrent_id: string; hash: string; file_id: number; path: string; bytes: number; source_name: string; status: 'draft'; created_at: string }
export interface SavedMoviePage { movies: Movie[]; next_after?: string }
const record = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null;
const imdb = (v: unknown): v is string => typeof v === 'string' && /^tt[0-9]{7,12}$/.test(v);
const uuid = (v: unknown): v is string => typeof v === 'string' && /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(v);
const invalid = () => new ApiError('TED returned incomplete movie information. Refresh to check the current state.');
export function movie(v: unknown): Movie {
  if (!record(v) || !imdb(v.id) || v.type !== 'movie' || typeof v.name !== 'string' || !v.name.trim() || typeof v.year !== 'string' || typeof v.description !== 'string' || typeof v.poster !== 'string' || !Array.isArray(v.genres) || !v.genres.every(g => typeof g === 'string')) throw invalid();
  if (v.poster && !titleArtworkURL(v.poster)) throw invalid();
  return v as unknown as Movie;
}
function mapping(v: unknown): Mapping {
  if (!record(v) || !uuid(v.id) || !uuid(v.movie_id) || typeof v.rd_account_id !== 'string' || !/^\d+$/.test(v.rd_account_id) || typeof v.torrent_id !== 'string' || typeof v.hash !== 'string' || !/^[a-f0-9]{40}$/.test(v.hash) || !Number.isSafeInteger(v.file_id) || Number(v.file_id) <= 0 || typeof v.path !== 'string' || !v.path.startsWith('/') || !Number.isSafeInteger(v.bytes) || Number(v.bytes) <= 0 || typeof v.source_name !== 'string' || v.status !== 'draft' || typeof v.created_at !== 'string' || !Number.isFinite(Date.parse(v.created_at))) throw invalid();
  return { ...v, movie: movie(v.movie) } as Mapping;
}
export const catalogApi = {
  browse: async (search = '', skip = 0, signal?: AbortSignal, filters:DiscoveryFilters = {}): Promise<MoviePage> => {
    const v = await request(`/api/movies?search=${encodeURIComponent(search)}&skip=${skip}${discoveryQuery(filters)}`, 'GET', undefined, signal);
    if (!record(v) || v.skip !== skip || !Array.isArray(v.movies)) throw invalid();
    const movies = v.movies.map(movie);
    if (new Set(movies.map(m => m.id)).size !== movies.length || (v.next_skip !== undefined && (movies.length === 0 || search !== '' || v.next_skip !== skip + 50))) throw invalid();
    return { movies, skip, next_skip: v.next_skip as number | undefined };
  },
  movie: async (id: string, signal?: AbortSignal): Promise<Movie> => {
    const m = movie(await request(`/api/movies/${encodeURIComponent(id)}`, 'GET', undefined, signal));
    if (m.id !== id) throw invalid(); return m;
  },
  mapping: async (id: string, signal?: AbortSignal): Promise<Mapping | null> => {
    const v = await request(`/api/admin/movies/${encodeURIComponent(id)}/mapping`, 'GET', undefined, signal);
    if (!record(v) || v.mapping === undefined) throw invalid();
    if (v.mapping === null) return null;
    const m = mapping(v.mapping); if (m.movie.id !== id) throw invalid(); return m;
  },
  save: async (id: string, source: TorrentDetail, file: TorrentFile, signal?: AbortSignal): Promise<MappingOperation> => mappingOperation(await request(`/api/admin/movies/${encodeURIComponent(id)}/mapping`, 'PUT', { torrent_id: source.id, hash: source.hash, file_id: file.id, path: file.path, bytes: file.bytes }, signal)),
  saved: async (after = '', signal?: AbortSignal): Promise<SavedMoviePage> => {
    const v = await request(`/api/movies/saved?after=${encodeURIComponent(after)}`, 'GET', undefined, signal);
    if (!record(v) || !Array.isArray(v.movies)) throw invalid();
    const movies = v.movies.map(movie);
    if (movies.some((m, i) => m.id <= (i ? movies[i - 1].id : after)) || (v.next_after !== undefined && (movies.length === 0 || v.next_after !== movies.at(-1)?.id))) throw invalid();
    return { movies, next_after: v.next_after as string | undefined };
  },
};
export function appendMovies(previous: MoviePage | null, incoming: MoviePage): MoviePage {
  if (incoming.skip === 0) return incoming;
  if (!previous || previous.next_skip !== incoming.skip || incoming.movies.some(m => previous.movies.some(p => p.id === m.id))) throw new ApiError('Cinemeta results changed while loading. Refresh to start again.');
  return { ...incoming, movies: [...previous.movies, ...incoming.movies] };
}
