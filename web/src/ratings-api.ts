import {ApiError,request} from './api.ts';
export interface Rating {id:string;rating:number|null;pending:boolean}
export async function ratings(kind:'movie'|'series',ids:string[],signal:AbortSignal):Promise<Rating[]>{
 const result:Rating[]=[];
 for(let i=0;i<ids.length;i+=100){
  const batch=ids.slice(i,i+100);
  const v=await request(`/api/catalog-ratings?kind=${kind}&ids=${batch.join(',')}`,'GET',undefined,signal) as {ratings?:Rating[]};
  if(!v||!Array.isArray(v.ratings)||v.ratings.length!==batch.length||new Set(v.ratings.map(r=>r?.id)).size!==batch.length||v.ratings.some(r=>!r||!batch.includes(r.id)||typeof r.pending!=='boolean'||(r.rating!==null&&(typeof r.rating!=='number'||!Number.isFinite(r.rating)||r.rating<=0||r.rating>10))))throw new ApiError('IMDb ratings are unavailable.');
  result.push(...v.ratings);
 }
 return result;
}
