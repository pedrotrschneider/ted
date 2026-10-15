import {ApiError,request} from './api.ts';
export interface LibraryRemoval{publication_mode:'native'; id:string;phase:''|'snapshot'|'removing'|'complete';error:string}
function removal(v:unknown):LibraryRemoval{
 const value=v as LibraryRemoval;
 if(!value||!['native'].includes(value.publication_mode)||typeof value.id!=='string'||!['','snapshot','removing','complete'].includes(value.phase)||typeof value.error!=='string')throw new ApiError('Library removal status is unavailable. Refresh before retrying.');
 return value;
}
export const libraryApi={
 status:async(kind:'movies'|'series',id:string,signal?:AbortSignal)=>removal(await request(`/api/admin/${kind}/${encodeURIComponent(id)}/library-removal`,'GET',undefined,signal)),
 remove:async(kind:'movies'|'series',id:string)=>removal(await request(`/api/admin/${kind}/${encodeURIComponent(id)}/library`,'DELETE')),
};
