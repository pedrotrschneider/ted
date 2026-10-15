export const genres = ['None','Action','Adventure','Animation','Biography','Comedy','Crime','Documentary','Drama','Family','Fantasy','History','Horror','Mystery','Romance','Sci-Fi','Sport','Thriller','War','Western'] as const;
export type DiscoveryCatalog = 'popular'|'new'|'featured';
export interface DiscoveryFilters {catalog?:DiscoveryCatalog;genre?:string;year?:number}
export function discoveryQuery(filters:DiscoveryFilters):string {
 const q=new URLSearchParams();
 if(filters.catalog)q.set('catalog',filters.catalog);
 if(filters.genre)q.set('genre',filters.genre);
 if(filters.year!==undefined)q.set('year',String(filters.year));
 const encoded=q.toString();return encoded?'&'+encoded:'';
}
export interface WatchFilterAccount {id:number;name:string;color:string}
export interface BrowseSelection {catalog:DiscoveryCatalog;genre:string;year:number;saved:boolean;watched:boolean;search:string;watchedAccounts?:number[]}
// Undefined follows all available accounts, while [] deliberately selects none.
export function toggleWatchAccount(selected:number[]|undefined,accounts:WatchFilterAccount[],id:number):number[]{
 const ids=accounts.filter(a=>selected===undefined||selected.includes(a.id)).map(a=>a.id);
 return ids.includes(id)?ids.filter(value=>value!==id):[...ids,id];
}
export function watchAccountTextColor(color:string):string {
 const rgb=[1,3,5].map(i=>parseInt(color.slice(i,i+2),16)/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4);
 return .2126*rgb[0]+.7152*rgb[1]+.0722*rgb[2]>.179?'#111111':'#FFFFFF';
}
export function discoveryFilters(selection:BrowseSelection):DiscoveryFilters {
 if(selection.search)return {};
 return selection.catalog==='new'?{catalog:'new',year:selection.year}:{catalog:selection.catalog,genre:selection.genre};
}

export function browseHeading(kind:'movie'|'series', selection:BrowseSelection):string {
 const media=kind==='movie'?'Movies':'Series';
 const personal=selection.saved||selection.watched;
 if(selection.search&&!personal)return `${media} matching “${selection.search}”`;
 const prefix=personal?[selection.saved?'Mapped':'',selection.watched?'Watched':''].filter(Boolean).join(' and '):selection.catalog==='new'?String(selection.year):selection.catalog==='featured'?'Featured':'Popular';
 const genre=personal||selection.catalog!=='new'?selection.genre:'';
 const heading=[prefix,genre,media].filter(Boolean).join(' ');
 return selection.search?`${heading} matching “${selection.search}”`:heading;
}
