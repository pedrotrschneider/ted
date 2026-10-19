export type Page = 'movies' | 'series' | 'watchlist' | 'ratings' | 'history' | 'account' | 'people' | 'sources' | 'stremio-sync';

// Keep existing title bookmarks while making every top-level view addressable.
export function pageFromHash(hash: string, admin: boolean): Page {
 if (/^#series(?:\/tt[0-9]{7,12})?$/.test(hash)) return 'series';
 if (/^#movie\/tt[0-9]{7,12}$/.test(hash) || hash === '#movies') return 'movies';
 if (hash === '#watchlist' || hash === '#ratings' || hash === '#history' || hash === '#account') return hash.slice(1) as Page;
 if (admin && (hash === '#people' || hash === '#sources' || hash === '#stremio-sync')) return hash.slice(1) as Page;
 return 'movies';
}
