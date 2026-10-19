import type {CSSProperties} from 'react';
import {ratingColors,ratingNames} from './personal-rating';
export function SavedIcon(){return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><path d="M7 3h10a2 2 0 0 1 2 2v16l-7-5-7 5V5a2 2 0 0 1 2-2Z"/></svg>}
const thumb='M3 10h4v11H3zm6 11V10l5-7c2 0 2 2 1 5h5c2 0 2 1 2 3l-3 9c0 1-1 1-2 1Z';
const palm='M2 23V13l1-4 1 1v4-9l1-1 1 1v7-10l1-1 1 1v10-8l1-1 1 1v10l2-3 1 1-2 7-3 4Z';
export function RatingIcon({rating}:{rating:number}){
 const hand=<path d={palm} transform="translate(.75 3.6) scale(.7)"/>;
 const hands=<>{hand}<g transform="translate(24 0) scale(-1 1)">{hand}</g></>;
 return <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">{rating===1?<><path d="M8 8c-2-2 7-3 4-7 6 2 8 7 4 8 4-1 6 4 3 5 5 0 5 7 0 7H5c-5 0-5-6-1-7-3-2 0-6 4-6Z"/><circle cx="9" cy="14" r="1.4" fill={ratingColors[0]}/><circle cx="15" cy="14" r="1.4" fill={ratingColors[0]}/></>:rating===4?<path d="M5 11h14v3H5Z"/>:rating===7?hands:<g transform={rating<4?'rotate(180 12 12)':undefined}>{rating===2||rating===6?<><path d={thumb} transform="translate(1 -1) scale(.68)"/><path d={thumb} transform="translate(6 9) scale(.68)"/></>:<path d={thumb}/>}</g>}</svg>;
}
export function RatingBadge({rating,small=false}:{rating:number;small?:boolean}){return <span className={`personal-rating${small?' personal-rating-small':''}`} style={{backgroundColor:ratingColors[rating-1]} as CSSProperties} role="img" aria-label={ratingNames[rating-1]} title={ratingNames[rating-1]}><RatingIcon rating={rating}/></span>}
