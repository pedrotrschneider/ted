import test from 'node:test';
import assert from 'node:assert/strict';
import {toggleWatchAccount,watchAccountTextColor} from '../src/discovery-filters.ts';
import {mediaBrowserApi} from '../src/media-browser-api.ts';
const accounts=[{id:1,name:'Pedro',color:'#FFFFFF'},{id:2,name:'Home',color:'#59C6F2'},{id:3,name:'Family',color:'#123456'}];
test('all accounts start selected, toggles permit any subset including none',()=>{
 let selected=toggleWatchAccount(undefined,accounts,1);assert.deepEqual(selected,[2,3]);
 selected=toggleWatchAccount(selected,accounts,2);assert.deepEqual(selected,[3]);
 selected=toggleWatchAccount(selected,accounts,3);assert.deepEqual(selected,[]);
 assert.deepEqual(toggleWatchAccount(selected,accounts,1),[1]);
 assert.deepEqual(toggleWatchAccount([1,99],accounts,2),[1,2]);
 assert.equal(watchAccountTextColor('#FFFFFF'),'#111111');assert.equal(watchAccountTextColor('#123456'),'#FFFFFF');
});
test('watched account selection distinguishes all, a subset and none on every page',async()=>{
 const original=globalThis.fetch;
 const selection={catalog:'popular' as const,genre:'',year:2026,saved:false,watched:true,search:''};
 try{for(const ids of [undefined,[2,3],[]]){
  globalThis.fetch=async url=>{const q=new URL(String(url),'https://ted.invalid').searchParams;assert.equal(q.get('watch_accounts'),ids===undefined?null:ids.join(','));assert.equal(q.get('after'),'tt7200100');return Response.json({titles:[],watch_accounts:accounts})};
  assert.deepEqual(await mediaBrowserApi.library('series',{...selection,watchedAccounts:ids},'tt7200100',new AbortController().signal),{titles:[],next:undefined,watchAccounts:accounts});
 }
 globalThis.fetch=async url=>{assert.equal(new URL(String(url),'https://ted.invalid').searchParams.has('watch_accounts'),false);return Response.json({titles:[],watch_accounts:accounts})};
 await mediaBrowserApi.library('movie',{...selection,watched:false,saved:true,watchedAccounts:[2]},'',new AbortController().signal);
 }finally{globalThis.fetch=original}
});
test('missing or malformed account choices fail rather than silently showing incomplete filters',async()=>{
 const original=globalThis.fetch;const selection={catalog:'popular' as const,genre:'',year:2026,saved:false,watched:true,search:''};
 try{for(const choices of [undefined,[],[accounts[0],accounts[0]],[{...accounts[0],color:'red'}],[{...accounts[0],id:0}]]){
 globalThis.fetch=async()=>Response.json({titles:[],watch_accounts:choices});
 await assert.rejects(mediaBrowserApi.library('movie',selection,'',new AbortController().signal),/account filters/);
 }}finally{globalThis.fetch=original}
});
