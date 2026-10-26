import {PersonalControls} from './PersonalTitles';
import {useTitleRatings} from './useTitleRatings';
import {WatchToggle,ForgetWatchButton} from './WatchToggle';
import type {WatchTitleControl} from './WatchToggle';
import { useState } from 'react';
import {TitleStatusBadge,useTitleStatuses} from './TitleStatus';
import type { MediaTitle } from './media-browser-api';
export function Poster({ title }: { title: {poster:string} }) {
  const [failed, setFailed] = useState(false);
  return title.poster && !failed ? <img className="poster" src={title.poster} alt="" loading="lazy" referrerPolicy="no-referrer" onError={() => setFailed(true)}/> : <div className="poster poster-empty" aria-hidden="true">TED</div>;
}
export function MediaHeader({title,watch}:{title:MediaTitle;watch:WatchTitleControl}) {
  const {states,error}=useTitleStatuses([title]);
  const ratings=useTitleRatings([title]),rating=ratings.values[title.id];
  return <section className="movie-detail"><div className="title-poster"><Poster key={title.poster} title={title}/><TitleStatusBadge status={states[title.id]} kind={title.type}/></div><div><p className="eyebrow">{title.type.toUpperCase()} · {title.year}</p><h1>{title.name}</h1>{error&&<p role="status" className="hint">{error}</p>}<p>{title.genres.join(' · ')}{rating!=null&&<span className="imdb-rating" aria-label={`IMDb rating: ${rating} out of 10`} title="IMDb rating via Cinemeta">{title.genres.length?' · ':''}IMDb {rating.toFixed(1)}</span>}</p>{ratings.error&&<p role="status" className="hint">{ratings.error}</p>}<p>{title.description || 'No description available.'}</p><p className="hint">Cinemeta · IMDb {title.id}</p><div className="actions media-actions"><WatchToggle control={watch} label={title.name}/>{states[title.id]?.watch.seen&&<ForgetWatchButton control={watch} label={title.name}/>}{states[title.id]&&<PersonalControls title={title} watchlist={states[title.id].watchlist} rating={states[title.id].user_rating}/>}<a className="dmm-link" href={`https://debridmediamanager.com/${title.type==='movie'?'movie':'show'}/${encodeURIComponent(title.id)}`} target="_blank" rel="noopener noreferrer" aria-label="Open Debrid Media Manager (opens in a new tab)"><img src="https://debridmediamanager.com/apple-touch-icon.png" width="28" height="28" alt="" referrerPolicy="no-referrer" onError={event=>{event.currentTarget.hidden=true}}/><span>Open Debrid Media Manager</span></a></div></div></section>;
}
export function MediaGrid({titles}:{titles:MediaTitle[]}) {
 const ratings=useTitleRatings(titles);
  const {states,error}=useTitleStatuses(titles);
  return <>{error&&<p role="status" className="hint">{error}</p>}{ratings.error&&<p role="status" className="hint">{ratings.error}</p>}<ul className="movie-grid">{titles.map(title=><li key={title.id}><a className="movie-card" href={`#${title.type}/${title.id}`}><div className="title-poster"><Poster key={title.poster} title={title}/><TitleStatusBadge status={states[title.id]} kind={title.type}/></div><strong>{title.name}</strong><span className="movie-card-meta">{ratings.values[title.id]!=null&&<span className="imdb-rating" aria-label={`IMDb rating: ${ratings.values[title.id]} out of 10`} title="IMDb rating via Cinemeta">★ {ratings.values[title.id]!.toFixed(1)}{title.year?' · ':''}</span>}<span>{title.year}</span></span></a></li>)}</ul></>;
}
