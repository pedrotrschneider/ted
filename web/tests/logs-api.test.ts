import test from 'node:test';
import assert from 'node:assert/strict';
import {logPage,logSettings} from '../src/logs-api.ts';
import {pageFromHash} from '../src/navigation.ts';
test('logs route is admin-only and settings retain the 90-day contract',()=>{
 assert.equal(pageFromHash('#logs',true),'logs');assert.equal(pageFromHash('#logs',false),'movies');
 assert.equal(logSettings({retention_days:90,stored_bytes:0,max_stored_bytes:1073741824,max_device_bytes:67108864}).retention_days,90);
 assert.throws(()=>logSettings({retention_days:0}));
});
test('query pages preserve correlation, pagination and literal diagnostic text',()=>{
 const entry={id:'entry',at:'2026-11-25T00:00:00Z',level:'error',component:'player',message:'<script>literal</script>',user_id:1,username:'viewer',device_id:'television',device_name:'Living room',platform:'android',app_version:'0.3.18',received_at:'2026-11-25T00:01:00Z',batch_seq:1,session_id:'session'};
 const page=logPage({entries:[entry],head_seq:2,next_cursor:'opaque'});
 assert.equal(page.entries[0].message,entry.message);assert.equal(page.next_cursor,'opaque');
 assert.throws(()=>logPage({entries:[{...entry,message:123}],head_seq:2}));
 assert.throws(()=>logPage({entries:[entry],head_seq:'2'}));
});
