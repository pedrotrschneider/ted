// Browser artwork is either Cinemeta's public image or a versioned TED cache
// route. No arbitrary relative URL, origin, query or credentials are accepted.
export function titleArtworkURL(value:string):boolean{
 if(/^\/api\/metadata\/images\/[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}\?v=[a-f0-9]{64}$/.test(value))return true;
 try{const u=new URL(value);return u.protocol==='https:'&&!u.username&&!u.password&&!u.port&&['images.metahub.space','live.metahub.space','m.media-amazon.com','image.tmdb.org'].includes(u.hostname)}catch{return false}
}
