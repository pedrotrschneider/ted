import { request, ApiError } from './api.ts';
import { mappingOperation } from './mapping-operation-api.ts';
import type { EpisodeDraft } from './episode-draft.ts';
// One submission owns the complete draft, including all selected torrents.
export async function saveEpisodeDraft(id:string,category:'shows'|'anime',draft:EpisodeDraft){
 const files=Object.entries(draft).map(([episode_id,{source,file}])=>{
 if(!file.video||file.bytes<=0||!source.files.some(f=>f.id===file.id&&f.path===file.path&&f.bytes===file.bytes))throw new ApiError('A selected file changed. Refresh and review the mapping.');
 return {episode_id,torrent_id:source.id,hash:source.hash,file_id:file.id,path:file.path,bytes:file.bytes};
 });
 return mappingOperation(await request(`/api/admin/series/${encodeURIComponent(id)}/mappings`,'PUT',{category,files}));
}
