import { useLayoutEffect, useRef, useState } from 'react';
import { errorMessage } from './api';
import { mergeSourceAssignments, sourceAssignments, seasonFileIDs, nextUnmatchedFile } from './episode-draft';
import type { EpisodeDraft } from './episode-draft';
import type { Series, SeriesMappings } from './series-api';
import type { TorrentDetail } from './rd-api';
import { assignEpisode, matchPattern, orderedEpisodes, orderedFiles } from './episode-matching';
import { assignSeasonStep, seasonSteps, skipSeasonStep, skipMappingStep } from './season-step';
import type { EpisodeAssignments } from './episode-matching';
type Mode='Manual'|'Season Range'|'Pattern';
export function EpisodeMappingEditor({show,source,existing,allDraft,changeDraft}:{show:Series;source:TorrentDetail;existing:SeriesMappings;allDraft:EpisodeDraft;changeDraft:(draft:EpisodeDraft)=>void}) {
 const fileList=useRef<HTMLUListElement>(null),scrollTarget=useRef<number|undefined>(undefined);
 useLayoutEffect(()=>{
  const id=scrollTarget.current;scrollTarget.current=undefined;
  const list=fileList.current;if(id===undefined||!list)return;
  const item=list.querySelector<HTMLElement>(`[data-file-id="${id}"]`);if(!item)return;
  const top=list.scrollTop+item.getBoundingClientRect().top-list.getBoundingClientRect().top-list.clientTop-(list.clientHeight-item.getBoundingClientRect().height)/2;
  list.scrollTo({top,behavior:window.matchMedia('(prefers-reduced-motion: reduce)').matches?'instant':'smooth'});
 });
 const [mode,setMode]=useState<Mode>('Manual');
 const draft=sourceAssignments(allDraft,source);
 function setDraft(next:EpisodeAssignments|((old:EpisodeAssignments)=>EpisodeAssignments)){changeDraft(mergeSourceAssignments(allDraft,source,typeof next==='function'?next(draft):next))}
 const [cursor,setCursor]=useState(()=>{const available=orderedEpisodes(show.episodes);const first=available.findIndex(e=>!allDraft[e.id]);return first<0?available.length:first}),[extras,setExtras]=useState(false);
 const [pattern,setPattern]=useState('S{%s.2}E{%e.2}'),[message,setMessage]=useState(''),[error,setError]=useState('');
 const episodes=orderedEpisodes(show.episodes),occupied=new Set(existing.mappings.map(m=>m.episode_id));
 const manual=episodes,current=manual[cursor];
 const seasons=seasonSteps(episodes);
 const [seasonIndex,setSeasonIndex]=useState(0);
 const season=seasons[seasonIndex];
 const seasonOptions=seasons.map(value=><option key={value} value={value}>{value===0?'Specials':`Season ${value}`}</option>);
 function navigateEpisode(index:number){if(index<0)return;setCursor(index);setError('');setMessage('')}
 function navigateSeason(value:number){const index=seasons.indexOf(value);if(index<0)return;setSeasonIndex(index);setError('');setMessage('')}
 const files=orderedFiles(source.files,extras),seasonEpisodes=episodes.filter(e=>e.season===season);
 const selected=Object.keys(allDraft).length;
 const range=seasonFileIDs(allDraft,source,seasonEpisodes.map(e=>e.id),existing.mappings);
 function choose(file:number) {
  setMessage('');setError('');
  if(mode==='Manual'&&current){setDraft(d=>assignEpisode(d,current.id,file));setCursor(c=>c+1)}
  if(mode==='Season Range'){try{const next=assignSeasonStep(allDraft,source,files,episodes,seasonIndex,file,new Set<string>());const last=files[files.findIndex(f=>f.id===file)+seasonEpisodes.length-1];scrollTarget.current=nextUnmatchedFile(files,last.id,source,next.draft,existing.mappings);changeDraft(next.draft);setSeasonIndex(next.index);setMessage(`Range assigned to ${season===0?'Specials':`Season ${season}`}.`)}catch(e){setError(errorMessage(e))}}
 }
 function patternMatch(){try{const result=matchPattern(files,episodes,pattern,draft,new Set([...occupied,...Object.keys(allDraft)]));setDraft(result.assignments);setError('');setMessage(`Matched ${result.added} new episodes. ${result.ambiguous} ambiguous episodes left unassigned. Existing selections kept.`)}catch(e){setError(errorMessage(e))}}
 return <div className="mapping-modal-columns"><div className="mapping-controls"><p className="hint">Changes stay in your draft when you close this window.</p><h3 className="source-name" title={source.name}>{source.name}</h3>

 <fieldset className="mapping-choice"><legend>Matching mode</legend><div className="mapping-radios">{(['Manual','Season Range','Pattern'] as const).map(value=><label className="check" key={value}><input type="radio" name="mapping-mode" checked={mode===value} onChange={()=>{setMode(value);setError('');setMessage('')}}/>{value}</label>)}</div></fieldset>
 <div className="mapping-prompt">
 {mode==='Manual'&&<><h4 className="mapping-navigation"><span>{current?'Select file for':'All episodes reviewed'}</span><select aria-label="Manual season" value={current?.season??''} onChange={e=>navigateEpisode(manual.findIndex(episode=>episode.season===Number(e.target.value)))}>{!current&&<option value="" disabled>Choose season</option>}{seasonOptions}</select><select aria-label="Manual episode" value={current?.id??''} disabled={!current} onChange={e=>navigateEpisode(manual.findIndex(episode=>episode.id===e.target.value))}>{!current&&<option value="" disabled>Choose episode</option>}{manual.filter(episode=>episode.season===current?.season).map(episode=><option key={episode.id} value={episode.id}>Episode {episode.number}</option>)}</select></h4>{current&&<p className="mapping-episode-name" title={current.name}>{current.name} · {cursor+1} of {manual.length}</p>}<div className="actions"><button type="button" disabled={cursor===0} onClick={()=>setCursor(c=>c-1)}>Previous episode</button><button type="button" disabled={!current} onClick={()=>{setCursor(skipMappingStep(allDraft,cursor,manual.length).index)}}>Skip episode</button></div><p className="hint">Selecting a file advances to the next episode. Selected files stay selectable for multi-episode files. Skipping keeps existing assignments. Specials come last.</p></>}
 {mode==='Season Range'&&<><h4 className="mapping-navigation"><span>{season===undefined?'All seasons reviewed':'Select the starting file for'}</span><select aria-label="Season range season" value={season??''} onChange={e=>navigateSeason(Number(e.target.value))}>{season===undefined&&<option value="" disabled>Choose season</option>}{seasonOptions}</select></h4>{season!==undefined&&<p>{seasonIndex+1} of {seasons.length} · Click to assign {seasonEpisodes.length} consecutive video files and advance to the next season.</p>}<div className="actions"><button type="button" disabled={seasonIndex===0} onClick={()=>{setSeasonIndex(i=>i-1);setError('');setMessage('')}}>Previous season</button><button type="button" disabled={season===undefined} onClick={()=>{const next=skipSeasonStep(allDraft,episodes,seasonIndex);setSeasonIndex(next.index);setError('');setMessage('')}}>Skip season</button></div><p className="hint">Specials come last. Go back to revise a range. Yellow files belong to the current season; other assignments stay green. Skipping keeps all existing assignments.</p></>}

 {mode==='Pattern'&&<><div className="mapping-pattern-entry"><label>Pattern<input value={pattern} maxLength={256} onChange={e=>setPattern(e.target.value)} placeholder="S{%s}xE{%e.3}"/></label><button type="button" onClick={patternMatch}>Match</button></div><p className="hint">{'S{%s}xE{%e.3} matches S3xE023 for Season 3 Episode 23. Padding is a minimum width. Matching uses literal filename text and only fills unassigned episodes.'}</p></>}
 </div>
 <div className="actions"><button type="button" disabled={!selected&&cursor===0&&seasonIndex===0} onClick={()=>{changeDraft({});setCursor(0);setSeasonIndex(0);setError('');setMessage('Draft mapping cleared. Saved mappings kept.')}}>Clear mapping</button></div>
 <p className="hint">Video files in filename order. Color and episode labels indicate selections.</p>
 {message&&<p role="status" className="notice">{message}</p>}{error&&<p role="alert" className="notice error">{error}</p>}
 <p className="mapping-assignment-count">{selected} draft assignments across all sources · {occupied.size} saved.</p></div><div className="mapping-file-column"><h2 className="mapping-files-mobile-heading">Video files</h2><label className="check"><input type="checkbox" checked={extras} onChange={e=>{setExtras(e.target.checked)}}/>Show Extra/sample video files</label><ul className="mapping-files" ref={fileList}>{files.map(file=>{const assigned=episodes.filter(e=>draft[e.id]===file.id);const stored=existing.mappings.filter(m=>m.torrent_id===source.id&&m.hash===source.hash&&m.file_id===file.id);const inRange=mode==='Season Range'&&range.includes(file.id);return <li key={file.id} data-file-id={file.id}><button type="button" className={`${assigned.length||stored.length?'is-mapped ':''}${inRange?'in-range ':''}${mode==='Manual'&&current&&draft[current.id]===file.id?'is-current':''}`} aria-pressed={mode==='Season Range'?inRange:!!(assigned.length||stored.length)} disabled={mode==='Pattern'||(mode==='Manual'&&!current)||(mode==='Season Range'&&season===undefined)} onClick={()=>choose(file.id)}><strong>{file.path.replace(/^\//,'')}</strong><span>{(file.bytes/1048576).toFixed(1)} MiB · {file.selected?'Selected in RD':'Not selected in RD'}</span>{assigned.length>0&&<span>Mapped: {assigned.map(e=>`S${e.season}E${e.number}`).join(', ')}</span>}{stored.length>0&&<span>Already saved for {stored.length} episode(s)</span>}{inRange&&<span>Selected range</span>}</button></li>})}</ul>

 </div></div>;
}
