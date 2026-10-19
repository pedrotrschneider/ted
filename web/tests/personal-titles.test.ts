import test from 'node:test';
import assert from 'node:assert/strict';
import {preferences,setTitlePreference,collection} from '../src/personal-titles-api.ts';
import type {MediaTitle} from '../src/media-browser-api.ts';
import {parseTitleStatuses} from '../src/title-status-api.ts';
const movie={type:'movie',id:'tt1234567',name:'Title',poster:'',year:'2026',genres:[],description:''} as MediaTitle;
test('mandatory preferences and seven-point ratings reject missing or malformed fields',()=>{
 assert.deepEqual(preferences({watchlist:true,user_rating:7}),{watchlist:true,user_rating:7});
 for(const raw of [{watchlist:false},{user_rating:null},{watchlist:false,user_rating:0},{watchlist:false,user_rating:8},{watchlist:false,user_rating:2.5}])assert.throws(()=>preferences(raw));
 const status={id:movie.id,publication_mode:'native',watch:{seen:false,watched:0,available:1,progress:null},state:'',added:0,total:1,phase:'',validated:0};
 assert.throws(()=>parseTitleStatuses({statuses:[status]},[movie.id]));
 assert.equal(parseTitleStatuses({statuses:[{...status,watchlist:false,user_rating:null}]},[movie.id]).length,1);
});
test('explicit mutations touch one preference and require a complete confirmation',async()=>{
 const old=globalThis.fetch;const bodies:unknown[]=[];let complete=true;
 globalThis.fetch=async(input,init)=>{assert.match(String(input),/preferences\/(watchlist|rating)$/);bodies.push(JSON.parse(init!.body as string));return Response.json(complete?{watchlist:true,user_rating:null}:{watchlist:true})};
 try{await setTitlePreference(movie,'watchlist',true);await setTitlePreference(movie,'rating',null);assert.deepEqual(bodies,[{watchlist:true},{user_rating:null}]);complete=false;await assert.rejects(setTitlePreference(movie,'rating',7));}finally{globalThis.fetch=old}
});
test('personal list paging retains movie/series identity and rejects incomplete results',async()=>{
 const old=globalThis.fetch;let value:unknown={titles:[movie],next_after:'opaque-cursor'};
 globalThis.fetch=async(input)=>{assert.match(String(input),/collections\/watchlist\?kind=movie&after=/);return Response.json(value)};
 try{const page=await collection('watchlist','movie','',new AbortController().signal);assert.equal(page.next,'opaque-cursor');assert.equal(page.titles[0].id,movie.id);
  for(const invalid of [{titles:[movie,movie],next_after:''},{titles:[],next_after:'invalid'},{titles:[]}]){value=invalid;await assert.rejects(collection('watchlist','movie','',new AbortController().signal));}
 }finally{globalThis.fetch=old}
});
