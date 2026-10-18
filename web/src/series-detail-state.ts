import type {Series,SeriesMappings} from './series-api.ts';
interface Target {id:string;admin:boolean}
export interface SeriesDetailState extends Target {series:Series|null;mapping:SeriesMappings|null;error:string}
export type SeriesDetailAction = Target & ({type:'refresh'}|{type:'loaded';series:Series;mapping:SeriesMappings|null}|{type:'failed';error:string});
export function seriesDetailState(state:SeriesDetailState,action:SeriesDetailAction):SeriesDetailState {
 const same=state.id===action.id&&state.admin===action.admin;
 // Refreshing the same title must preserve the mounted progress observer and
 // last confirmed mappings. Clearing them creates a batch-completion reload loop.
 if(action.type==='refresh')return same?{...state,error:''}:{id:action.id,admin:action.admin,series:null,mapping:null,error:''};
 if(!same)return state;
 if(action.type==='failed')return {...state,error:action.error};
 return {...state,series:action.series,mapping:action.mapping,error:''};
}
