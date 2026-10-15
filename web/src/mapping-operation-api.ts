import { ApiError, request } from './api.ts';
export interface MappingOperation {publication_mode:'native'; id:string;phase:''|'queued'|'validating'|'publishing'|'complete'|'failed';error:string;total:number;validated:number;batch:number;batches:number;completed_batches:number;committed:number;retryable:boolean}
export function mappingOperation(value:unknown):MappingOperation{
 const v=value as MappingOperation;
 if(!v||!['native'].includes(v.publication_mode)||typeof v.id!=='string'||!['','queued','validating','publishing','complete','failed'].includes(v.phase)||typeof v.error!=='string'||!Number.isSafeInteger(v.total)||v.total<0||!Number.isSafeInteger(v.validated)||v.validated<0||v.validated>v.total||!Number.isSafeInteger(v.batch)||!Number.isSafeInteger(v.batches)||v.batch<0||v.batch>v.batches||!Number.isSafeInteger(v.completed_batches)||v.completed_batches<0||v.completed_batches>v.batches||!Number.isSafeInteger(v.committed)||v.committed<0||v.committed>v.validated||typeof v.retryable!=='boolean')throw new ApiError('Mapping outcome is unknown. Refresh its status before retrying.');return v;
}
export const mappingOperationApi={
 status:async(kind:'movies'|'series',id:string,signal?:AbortSignal)=>mappingOperation(await request(`/api/admin/${kind}/${encodeURIComponent(id)}/mapping-operation`,'GET',undefined,signal)),
 retry:async(kind:'movies'|'series',id:string,submission:string)=>mappingOperation(await request(`/api/admin/${kind}/${encodeURIComponent(id)}/mapping-operation/retry`,'POST',{submission_id:submission}))
};
export function mappingProgressText(state:MappingOperation):string{
 if(state.phase==='complete')return 'Mappings validated and ready in TED.';
 if(state.phase==='failed')return state.committed>0?`Stopped at batch ${state.batch} of ${state.batches}. ${state.committed} of ${state.total} episodes saved; completed batches remain available. Unfinished batches kept their previous mappings.`:'Mapping failed. Previous mappings and local copies were preserved.';
 const phase=state.phase==='queued'?'Waiting to validate the selected sources':state.phase==='validating'?`Validating media: ${state.validated} of ${state.total}`:'Activating validated mappings in TED';
 return state.batches>1?`Batch ${state.batch} of ${state.batches} · ${state.committed} of ${state.total} episodes saved. ${phase}`:phase;
}
