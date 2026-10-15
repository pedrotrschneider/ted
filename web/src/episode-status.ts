import type {LocalState} from './local-api';
import type {StreamState} from './streaming-api';
export function readinessBadge(state:StreamState|null){
	if(state){
	 if(state.adding||['queued','preparing','probing'].includes(state.phase))return {tone:'pending',label:state.queued?'Adding to TED · queued':'Adding to TED · validating'};
	 return state.published?{tone:'success',label:'Ready in TED'}:{tone:'error',label:'Not ready in TED'};
	}
 return {tone:'unknown',label:'Playback readiness unknown'};
}
export function episodeDownload(state:LocalState){
 const active=['queued','downloading','validating','publishing','removing'].includes(state.phase);
 const transferred=state.phase==='validating'?state.validated_bytes:state.completed_bytes;
 return {
  active,
  showProgress:['queued','downloading','validating','publishing','cancelled','failed'].includes(state.phase),
  percent:state.total_bytes>0?Math.min(100,Math.floor(transferred/state.total_bytes*100)):null,
  phaseLabel:state.phase==='validating'?'Verifying local copy':state.phase==='publishing'?'Publishing local copy':'Download progress',
  canDownload:['absent','cancelled','failed'].includes(state.phase),
  canPause:['queued','downloading','validating','publishing'].includes(state.phase),
  canRemove:state.phase!=='absent'&&(state.phase!=='removing'||!!state.error),
 };
}
