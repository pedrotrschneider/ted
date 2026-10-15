import { ApiError, request } from './api.ts';
export interface LocalState {
  phase: 'absent' | 'queued' | 'downloading' | 'validating' | 'publishing' | 'ready' | 'failed' | 'cancelled' | 'removing';
  completed_bytes: number; validated_bytes: number; total_bytes: number;
  speed_mbps: number | null; remaining_seconds: number | null;
  error: string; configured: boolean; eligible: boolean; mapped: boolean;
}
export const localApi = {
  async status(id: string, signal?: AbortSignal): Promise<LocalState> {
    const v = await request(`/api/${id.includes(':') ? 'episodes' : 'movies'}/${encodeURIComponent(id)}/local`, 'GET', undefined, signal) as LocalState;
    if (!v || !['absent','queued','downloading','validating','publishing','ready','failed','cancelled','removing'].includes(v.phase) || typeof v.error !== 'string' || typeof v.configured !== 'boolean' || typeof v.eligible !== 'boolean' || typeof v.mapped !== 'boolean' || [v.completed_bytes,v.validated_bytes,v.total_bytes].some(n => !Number.isSafeInteger(n) || n < 0) || [v.speed_mbps,v.remaining_seconds].some(n => n !== null && (typeof n !== 'number' || !Number.isFinite(n) || n < 0)) || (v.remaining_seconds !== null && !Number.isSafeInteger(v.remaining_seconds)) || v.completed_bytes > v.total_bytes || v.validated_bytes > v.completed_bytes) throw new ApiError('Invalid local file status. Refresh to check TED.');
    return v;
  },
  async change(id: string, action: 'download' | 'cancel' | 'remove'): Promise<void> {
    const url = action === 'download' ? `/api/${id.includes(':') ? 'episodes' : 'movies'}/${encodeURIComponent(id)}/local` : `/api/admin/${id.includes(':') ? 'episodes' : 'movies'}/${encodeURIComponent(id)}/local${action === 'cancel' ? '/cancel' : ''}`;
    const v = await request(url, action === 'remove' ? 'DELETE' : 'POST') as {status?: string};
    if (v?.status !== ({ download:'queued', cancel:'cancelled', remove:'removing' })[action]) throw new ApiError('Local file change was not confirmed. Refresh its status.');
  },
};

export interface DownloadBatch {total:number;queued:number;existing:number;missing:number}
export async function downloadSeries(id:string,season:number|undefined,readyOnly:boolean):Promise<DownloadBatch>{
 const v=await request(`/api/series/${encodeURIComponent(id)}/local`,'POST',{season,ready_only:readyOnly}) as DownloadBatch;
 if(!v||![v.total,v.queued,v.existing,v.missing].every(n=>Number.isSafeInteger(n)&&n>=0)||v.total===0||v.total!==v.queued+v.existing+v.missing)throw new ApiError('Download batch was not confirmed. Refresh episode statuses before retrying.');
 return v;
}
