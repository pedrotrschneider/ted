import { ApiError, request } from './api.ts';
export interface StreamState {publication_mode:'native'; queued?:boolean; mapped:boolean; validated:boolean; adding: boolean; phase: 'draft' | 'queued' | 'preparing' | 'probing' | 'ready' | 'failed' | 'cancelled'; error: string; published: boolean; job_id: number | null }
export const streamingApi = {
  async status(id: string, signal?: AbortSignal): Promise<StreamState> {
    const value = await request(`/api/admin/${id.includes(':') ? 'episodes' : 'movies'}/${encodeURIComponent(id)}/streaming`, 'GET', undefined, signal) as StreamState;
    if (!value || !['native'].includes(value.publication_mode) || (value.queued !== undefined && typeof value.queued !== 'boolean') || typeof value.adding !== 'boolean' || typeof value.mapped !== 'boolean' || typeof value.validated !== 'boolean' || !['draft','queued','preparing','probing','ready','failed','cancelled'].includes(value.phase) || typeof value.error !== 'string' || typeof value.published !== 'boolean' || value.published !== (value.phase === 'ready') || (value.job_id !== null && (!Number.isSafeInteger(value.job_id) || value.job_id <= 0))) throw new ApiError('Invalid preparation status. Refresh to check TED.');
    return value;
  },
  async change(id: string, cancel: boolean): Promise<void> {
    const result = await request(`/api/admin/${id.includes(':') ? 'episodes' : 'movies'}/${encodeURIComponent(id)}/streaming`, cancel ? 'DELETE' : 'POST') as { status?: string } | null;
    if (result?.status !== (cancel ? 'cancelled' : 'queued')) throw new ApiError('Preparation change was not confirmed. Refresh its status.');
  },
};
export interface BatchPreparation { total:number; queued:number; existing:number; missing:number }
export async function prepareSeries(id:string,season:number|undefined,mappedOnly:boolean):Promise<BatchPreparation>{
 const value=await request(`/api/admin/series/${encodeURIComponent(id)}/streaming`,'POST',{season,mapped_only:mappedOnly}) as BatchPreparation;
 if(!value||![value.total,value.queued,value.existing,value.missing].every(n=>Number.isSafeInteger(n)&&n>=0)||value.total===0||value.total!==value.queued+value.existing+value.missing)throw new ApiError('Batch preparation was not confirmed. Check episode statuses before retrying.');
 return value;
}
