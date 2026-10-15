import test from 'node:test';import assert from 'node:assert/strict';
const watchAccounts=[{id:1,name:'Main account',color:'#FFFFFF'}];
import {mediaBrowserApi,appendBrowse} from '../src/media-browser-api.ts';
test('shared browsing adapts movies and series to the same pagination contract',async()=>{
 const original=globalThis.fetch;try{for(const kind of ['movie','series'] as const){const title={id:'tt1245698',type:kind,name:'Title',year:'2008',description:'',poster:'',genres:[]};
 globalThis.fetch=async(url)=>{assert.equal(url,`/api/${kind==='movie'?'movies':'series'}?search=&skip=0`);return Response.json({[kind==='movie'?'movies':'series']:[title],skip:0,next_skip:50})};
 const page=await mediaBrowserApi.browse(kind,'',0,new AbortController().signal);assert.deepEqual(page,{titles:[title],skip:0,next:50});
 assert.throws(()=>appendBrowse(page,{titles:[title],skip:50,next:100}));assert.equal(page.titles.length,1);
 assert.deepEqual(appendBrowse(page,{titles:[],skip:50}).titles,[title]);}}
 finally{globalThis.fetch=original}
});
test('saved movies use the signed-in metadata endpoint and validate cursor boundaries',async()=>{
 const original=globalThis.fetch;const movie={id:'tt1345836',type:'movie',name:'Title',year:'2012',description:'',poster:'',genres:[]};
 try {
  globalThis.fetch=async(url)=>{assert.equal(url,'/api/movies/saved?after=');return Response.json({movies:[movie],next_after:movie.id})};
  assert.deepEqual(await mediaBrowserApi.saved('movie','',new AbortController().signal),{titles:[movie],next:movie.id});
  for(const response of [{movies:[movie],next_after:'tt9999999'},{movies:[movie,movie]},{mappings:[]},{movies:null}]){
   globalThis.fetch=async()=>Response.json(response);
   await assert.rejects(mediaBrowserApi.saved('movie','',new AbortController().signal),/incomplete/);
  }
  globalThis.fetch=async()=>Response.json({movies:[movie]});
  await assert.rejects(mediaBrowserApi.saved('movie',movie.id,new AbortController().signal),/incomplete/);
  globalThis.fetch=async()=>Response.json({movies:[]});
  assert.deepEqual(await mediaBrowserApi.saved('movie',movie.id,new AbortController().signal),{titles:[],next:undefined});
 }finally{globalThis.fetch=original}
});
test('discovery routes genre and release-year selections for both media kinds',async()=>{
 const original=globalThis.fetch;
 try {for(const kind of ['movie','series'] as const){for(const filters of [{catalog:'featured' as const,genre:'Sci-Fi'},{catalog:'new' as const,year:2025}]){
  globalThis.fetch=async(url)=>{const q=new URL(String(url),'https://ted.invalid').searchParams;assert.equal(q.get('catalog'),filters.catalog);assert.equal(q.get('genre'),filters.genre??null);assert.equal(q.get('year'),filters.year?String(filters.year):null);return Response.json({[kind==='movie'?'movies':'series']:[],skip:0})};
  assert.deepEqual((await mediaBrowserApi.browse(kind,'',0,new AbortController().signal,filters)).titles,[]);
 }}}finally{globalThis.fetch=original}
});
test('saved and watched send both filters and reject malformed or overlapping collection pages',async()=>{
 const original=globalThis.fetch;const selection={catalog:'popular' as const,genre:'Action',year:2026,saved:true,watched:true,search:'Batman'};
 const title={id:'tt1345836',type:'movie',name:'Title',year:'2012',description:'',poster:'',genres:[]};
 try {
  globalThis.fetch=async(url)=>{const q=new URL(String(url),'https://ted.invalid').searchParams;assert.equal(q.get('saved'),'true');assert.equal(q.get('watched'),'true');assert.equal(q.get('search'),'Batman');assert.equal(q.get('genre'),'Action');return Response.json({titles:[title],next_after:title.id,watch_accounts:watchAccounts})};
  assert.deepEqual(await mediaBrowserApi.library('movie',selection,'',new AbortController().signal),{titles:[title],next:title.id,watchAccounts});
  for(const body of [{titles:null},{titles:[title,title]},{titles:[title],next_after:'bad'}]){globalThis.fetch=async()=>Response.json(body);await assert.rejects(mediaBrowserApi.library('movie',selection,'',new AbortController().signal));}
  globalThis.fetch=async()=>Response.json({titles:[title]});await assert.rejects(mediaBrowserApi.library('movie',selection,title.id,new AbortController().signal));
 }finally{globalThis.fetch=original}
});

test('personal pages retain alphabetical server order even when IDs descend',async()=>{
 const original=globalThis.fetch;
 const selection={catalog:'popular' as const,genre:'',year:2026,saved:true,watched:true,search:''};
 try {for(const kind of ['movie','series'] as const){
  const titles=[{id:'tt9999999',type:kind,name:'Alpha',year:'',description:'',poster:'',genres:[]},{id:'tt1111111',type:kind,name:'Zulu',year:'',description:'',poster:'',genres:[]}];
  globalThis.fetch=async()=>Response.json({titles,next_after:titles[1].id,watch_accounts:watchAccounts});
  assert.deepEqual(await mediaBrowserApi.library(kind,selection,'tt8888888',new AbortController().signal),{titles,next:titles[1].id,watchAccounts});
 }}finally{globalThis.fetch=original}
});
